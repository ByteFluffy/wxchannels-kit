package cmd

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"wx_channel/internal/config"
	"wx_channel/internal/mcpserver"
)

var mcp_api_base_url string

var mcp_cmd = &cobra.Command{
	Use: "mcp", Short: "Serve the personal-use MCP tools over stdio without starting capture", Args: cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		base := strings.TrimSpace(mcp_api_base_url)
		if base == "" {
			base = configured_api_base_url()
		}
		server, err := mcpserver.NewServer(mcpserver.Config{
			APIBaseURL: base, Version: Version, Input: command.InOrStdin(), Output: command.OutOrStdout(), ErrorOutput: command.ErrOrStderr(),
			AllowedTools: mcpserver.PersonalToolNames(),
		})
		if err != nil {
			return err
		}
		return server.Serve(command.Context())
	},
}

func init() {
	mcp_cmd.Flags().StringVar(&mcp_api_base_url, "api-base-url", "", "Local wxc service URL (does not start a service or read browser credentials)")
	root_cmd.AddCommand(mcp_cmd)
}

func configured_api_base_url() string {
	protocol := strings.TrimSpace(Cfg.GetString("api.protocol"))
	if protocol == "" {
		protocol = "http"
	}
	host := config.APIClientHostname(Cfg.GetString("api.hostname"))
	port := Cfg.GetInt("api.port")
	if port <= 0 {
		port = 2022
	}
	return fmt.Sprintf("%s://%s", protocol, net.JoinHostPort(host, strconv.Itoa(port)))
}
