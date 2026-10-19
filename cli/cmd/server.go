package cmd

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/omattsson/stackctl/cli/pkg/client"
	"github.com/omattsson/stackctl/cli/pkg/output"
	"github.com/spf13/cobra"
)

// serverInfo is the result of "server info". Ping and the UI config are
// public routes, so the command sends no credentials.
type serverInfo struct {
	URL                       string `json:"url" yaml:"url"`
	Reachable                 bool   `json:"reachable" yaml:"reachable"`
	PingMillis                int64  `json:"ping_ms" yaml:"ping_ms"`
	PingError                 string `json:"ping_error,omitempty" yaml:"ping_error,omitempty"`
	Title                     string `json:"title,omitempty" yaml:"title,omitempty"`
	DefaultInstanceTTLMinutes *int   `json:"default_instance_ttl_minutes,omitempty" yaml:"default_instance_ttl_minutes,omitempty"`
	MaxInstanceTTLDays        int    `json:"max_instance_ttl_days" yaml:"max_instance_ttl_days"` // 0: unknown (ui-config failed)
	MaxInstanceTTLFromServer  bool   `json:"max_instance_ttl_from_server" yaml:"max_instance_ttl_from_server"`
	UIConfigError             string `json:"ui_config_error,omitempty" yaml:"ui_config_error,omitempty"`
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Show information about the k8s-stack-manager server",
}

var serverInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show the server URL, reachability and TTL settings",
	Long: `Show the server URL, the ping result and latency, the default TTL of new
stacks and the TTL limit (GET /api/v1/ping and GET /api/v1/ui-config).

Both routes are public: the command works without login and sends no
credentials. A server before k8s-stack-manager v0.12.0 does not send the
TTL limit; the command then shows the default of 30 days.

The command exits with a non-zero code when the server is not reachable.

Examples:
  stackctl server info
  stackctl server info -o json`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL := resolveAPIURL()
		if apiURL == "" {
			return errNoAPIURL
		}
		c := client.New(apiURL)
		if rootCmd.PersistentFlags().Changed("debug") {
			c.Debug = flagDebug
		} else {
			c.Debug = os.Getenv("STACKCTL_DEBUG") == "1"
		}
		if c.Debug {
			c.DebugWriter = os.Stderr
		}
		applyInsecureTLS(c)
		c.RetryBackoff = []time.Duration{} // one attempt: show the real reachability

		info := serverInfo{URL: c.BaseURL, MaxInstanceTTLDays: defaultMaxTTLDays}

		start := time.Now()
		pingErr := c.Ping()
		info.PingMillis = time.Since(start).Milliseconds()
		if pingErr != nil {
			info.PingError = pingErr.Error()
		} else {
			info.Reachable = true
			uiCfg, err := c.GetUIConfig()
			switch {
			case err == nil:
				info.Title = client.SanitizeServerText(uiCfg.Title)
				ttl := uiCfg.DefaultInstanceTTLMinutes
				info.DefaultInstanceTTLMinutes = &ttl
				if uiCfg.MaxInstanceTTLDays > 0 {
					info.MaxInstanceTTLDays = uiCfg.MaxInstanceTTLDays
					info.MaxInstanceTTLFromServer = true
				}
			case isStatus(err, http.StatusNotFound):
				// An older server without ui-config: the 30-day default.
			default:
				info.UIConfigError = err.Error()
				info.MaxInstanceTTLDays = 0
			}
		}

		if err := printServerInfo(info); err != nil {
			return err
		}
		if pingErr != nil {
			return fmt.Errorf("server %s is not reachable: %w", info.URL, pingErr)
		}
		return nil
	},
}

// printServerInfo prints the result of "server info".
func printServerInfo(info serverInfo) error {
	if printer.Quiet {
		return nil
	}
	switch printer.Format {
	case output.FormatJSON:
		return printer.PrintJSON(info)
	case output.FormatYAML:
		return printer.PrintYAML(info)
	}

	ping := fmt.Sprintf("ok (%d ms)", info.PingMillis)
	if !info.Reachable {
		ping = "failed: " + info.PingError
	}
	fields := []output.KeyValue{
		{Key: "Server", Value: info.URL},
		{Key: "Ping", Value: ping},
	}
	if info.Reachable {
		if info.Title != "" {
			fields = append(fields, output.KeyValue{Key: "Title", Value: info.Title})
		}
		defaultTTL := "-"
		switch {
		case info.DefaultInstanceTTLMinutes == nil:
		case *info.DefaultInstanceTTLMinutes == 0:
			defaultTTL = "no expiry"
		default:
			defaultTTL = strconv.Itoa(*info.DefaultInstanceTTLMinutes) + " minutes"
		}
		maxTTL := daysText(info.MaxInstanceTTLDays * 24 * 60)
		switch {
		case info.UIConfigError != "":
			maxTTL = "unknown"
		case !info.MaxInstanceTTLFromServer:
			maxTTL += " (default; the server does not send a limit)"
		}
		fields = append(fields,
			output.KeyValue{Key: "Default TTL", Value: defaultTTL},
			output.KeyValue{Key: "TTL limit", Value: maxTTL},
		)
		if info.UIConfigError != "" {
			fields = append(fields, output.KeyValue{Key: "UI config", Value: "failed: " + info.UIConfigError})
		}
	}
	return printer.PrintSingle(info, fields)
}

func init() {
	serverCmd.AddCommand(serverInfoCmd)
	rootCmd.AddCommand(serverCmd)
}
