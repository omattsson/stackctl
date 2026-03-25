package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionCmd_Bash(t *testing.T) {
	var buf bytes.Buffer
	completionCmd.SetOut(&buf)
	t.Cleanup(func() { completionCmd.SetOut(nil) })

	err := completionCmd.RunE(completionCmd, []string{"bash"})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "bash completion")
}

func TestCompletionCmd_Zsh(t *testing.T) {
	var buf bytes.Buffer
	completionCmd.SetOut(&buf)
	t.Cleanup(func() { completionCmd.SetOut(nil) })

	err := completionCmd.RunE(completionCmd, []string{"zsh"})
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

func TestCompletionCmd_Fish(t *testing.T) {
	var buf bytes.Buffer
	completionCmd.SetOut(&buf)
	t.Cleanup(func() { completionCmd.SetOut(nil) })

	err := completionCmd.RunE(completionCmd, []string{"fish"})
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

func TestCompletionCmd_Powershell(t *testing.T) {
	var buf bytes.Buffer
	completionCmd.SetOut(&buf)
	t.Cleanup(func() { completionCmd.SetOut(nil) })

	err := completionCmd.RunE(completionCmd, []string{"powershell"})
	require.NoError(t, err)
	assert.NotEmpty(t, buf.String())
}

func TestCompletionCmd_UnsupportedShell(t *testing.T) {
	err := completionCmd.RunE(completionCmd, []string{"tcsh"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported shell")
}
