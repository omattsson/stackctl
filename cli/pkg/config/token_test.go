package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *TokenStore {
	t.Helper()
	return &TokenStore{Context: "test", Dir: filepath.Join(t.TempDir(), "tokens")}
}

func TestTokenStore_SaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	exp := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)

	require.NoError(t, s.Save(&StoredToken{Token: "jwt", ExpiresAt: exp, Username: "alice", RefreshToken: "r1"}))

	got, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "jwt", got.Token)
	assert.Equal(t, "r1", got.RefreshToken)
	assert.Equal(t, "alice", got.Username)
	assert.True(t, exp.Equal(got.ExpiresAt))

	if runtime.GOOS != "windows" {
		path, _ := s.Path()
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
		dirInfo, err := os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())
	}

	// No temporary files are left behind.
	entries, err := os.ReadDir(s.Dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestTokenStore_LoadOldFileWithoutRefreshToken(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	require.NoError(t, os.MkdirAll(s.Dir, 0700))
	path, _ := s.Path()
	require.NoError(t, os.WriteFile(path, []byte(`{"token":"old-jwt","expires_at":"2030-01-01T00:00:00Z","username":"bob"}`), 0600))

	got, err := s.Load()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "old-jwt", got.Token)
	assert.Equal(t, "bob", got.Username)
	assert.Empty(t, got.RefreshToken)
}

func TestTokenStore_LoadMissingAndDelete(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	got, err := s.Load()
	require.NoError(t, err)
	assert.Nil(t, got)
	require.NoError(t, s.Delete(), "deleting a missing file is not an error")

	require.NoError(t, s.Save(&StoredToken{Token: "jwt"}))
	require.NoError(t, s.Delete())
	got, err = s.Load()
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestTokenStore_InvalidContext(t *testing.T) {
	t.Parallel()
	s := &TokenStore{Context: "../evil", Dir: t.TempDir()}
	_, err := s.Load()
	assert.Error(t, err)
	_, err = s.Lock()
	assert.Error(t, err)
}

func TestTokenStore_LockIsExclusive(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)

	unlock, err := s.Lock()
	require.NoError(t, err)

	acquired := make(chan func())
	go func() {
		u, err := s.Lock()
		if err != nil {
			close(acquired)
			return
		}
		acquired <- u
	}()

	select {
	case <-acquired:
		t.Fatal("second lock acquired while the first is held")
	case <-time.After(300 * time.Millisecond):
	}

	unlock()
	select {
	case u, ok := <-acquired:
		require.True(t, ok, "second lock failed")
		u()
	case <-time.After(5 * time.Second):
		t.Fatal("second lock not acquired after release")
	}
}

// TestTokenStore_LockAcrossProcesses runs a child test process that waits
// for the lock this process holds.
func TestTokenStore_LockAcrossProcesses(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("no subprocesses")
	}
	s := newTestStore(t)
	unlock, err := s.Lock()
	require.NoError(t, err)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLockProcess$")
	cmd.Env = append(os.Environ(), "STACKCTL_LOCK_HELPER_DIR="+s.Dir)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	lines := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	// The child reports "waiting" and then blocks on the lock.
	waitLine := func(want string) bool {
		timeout := time.After(10 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					return false
				}
				if l == want {
					return true
				}
			case <-timeout:
				return false
			}
		}
	}
	require.True(t, waitLine("waiting"), "child did not start")
	select {
	case l := <-lines:
		if l == "locked" {
			unlock()
			t.Fatal("child acquired the lock while the parent holds it")
		}
	case <-time.After(300 * time.Millisecond):
	}

	unlock()
	require.True(t, waitLine("locked"), "child did not acquire the lock after release")
	require.NoError(t, cmd.Wait())
}

// TestHelperLockProcess is the child of TestTokenStore_LockAcrossProcesses.
func TestHelperLockProcess(t *testing.T) {
	t.Parallel()
	dir := os.Getenv("STACKCTL_LOCK_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process only")
	}
	s := &TokenStore{Context: "test", Dir: dir}
	os.Stdout.WriteString("waiting\n")
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("locked\n")
	unlock()
}

func TestTokenStore_DefaultAPIURL(t *testing.T) {
	t.Parallel()
	s := newTestStore(t)
	s.DefaultAPIURL = "https://ctx.example.dev"

	require.NoError(t, s.Save(&StoredToken{Token: "a"}))
	got, err := s.Load()
	require.NoError(t, err)
	assert.Equal(t, "https://ctx.example.dev", got.APIURL, "file without api_url uses the context URL")

	require.NoError(t, s.Save(&StoredToken{Token: "b", APIURL: "https://login.example.dev"}))
	got, err = s.Load()
	require.NoError(t, err)
	assert.Equal(t, "https://login.example.dev", got.APIURL)
}

func TestRetryTransient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"success", nil, 1},
		{"not exist is not retried", fs.ErrNotExist, 1},
		{"other error is not retried", errors.New("boom"), 1},
		{"permission is retried", fs.ErrPermission, fileRetries},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			start := time.Now()
			err := retryTransient(func() error { calls++; return tt.err })
			assert.Equal(t, tt.wantCalls, calls)
			assert.ErrorIs(t, err, tt.err)
			// No sleep after the last attempt.
			assert.Less(t, time.Since(start), time.Duration(tt.wantCalls)*fileRetryDelay+40*time.Millisecond)
		})
	}
}

func TestRenameWithRetry_MissingDirFailsFast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	from := filepath.Join(dir, "a")
	require.NoError(t, os.WriteFile(from, []byte("x"), 0600))
	start := time.Now()
	err := renameWithRetry(from, filepath.Join(dir, "missing", "b"))
	require.Error(t, err)
	assert.Less(t, time.Since(start), fileRetryDelay)
}
