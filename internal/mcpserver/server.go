package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ricelines/chat/onboarding/internal/provisioner"
)

const (
	resourceModules       = "onboarding://modules"
	resourceUserAgents    = "onboarding://module/user_agents"
	resourceGetTool       = "onboarding://tool/onboarding.v1.user_agents.get"
	resourceProvisionTool = "onboarding://tool/onboarding.v1.user_agents.provision_initial"
)

type Server struct {
	server *mcp.Server
}

func NewServer(service *provisioner.Service) *Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "onboarding-provisioner",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "onboarding.v1.user_agents.get",
		Description: "List onboarding-managed user-agent provisioning records, optionally filtered by owner_matrix_user_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input provisioner.GetUserAgentsInput) (*mcp.CallToolResult, provisioner.GetUserAgentsOutput, error) {
		output, err := service.GetUserAgents(ctx, input)
		return nil, output, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "onboarding.v1.user_agents.provision_initial",
		Description: "Use this immediately when a Matrix owner says they want a new agent. Input normally only owner_matrix_user_id. Returns already_exists when the owner already has their onboarding-created default bot.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input provisioner.ProvisionInitialInput) (*mcp.CallToolResult, provisioner.ProvisionInitialOutput, error) {
		output, err := service.ProvisionInitial(ctx, input)
		return nil, output, err
	})

	result := &Server{server: server}
	result.registerResources()
	return result
}

func (s *Server) Handler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.server
	}, nil)
}

func (s *Server) registerResources() {
	s.server.AddResource(&mcp.Resource{
		URI:         resourceModules,
		Name:        "onboarding MCP modules",
		Description: "Top-level module index for the onboarding product provisioner.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, "# Onboarding MCP modules\n\n- `user_agents`: onboarding-managed user-agent provisioning. Resource: `onboarding://module/user_agents`\n"), nil
	})

	s.server.AddResource(&mcp.Resource{
		URI:         resourceUserAgents,
		Name:        "onboarding user_agents module",
		Description: "Provisioning record discovery and onboarding-default user-agent creation.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		body := "## user_agents\n\n" +
			"- `onboarding.v1.user_agents.get`: inspect provisioning records.\n" +
			"- `onboarding.v1.user_agents.provision_initial`: create or resume the initial onboarding bot for one owner.\n"
		return textResource(req.Params.URI, body), nil
	})

	s.server.AddResource(&mcp.Resource{
		URI:         resourceGetTool,
		Name:        "onboarding user_agents.get tool",
		Description: "Details for onboarding.v1.user_agents.get.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		body := "## onboarding.v1.user_agents.get\n\n" +
			"Input:\n- optional `owner_matrix_user_id`\n\n" +
			"Output:\n- `user_agents`: list of current provisioning records.\n"
		return textResource(req.Params.URI, body), nil
	})

	s.server.AddResource(&mcp.Resource{
		URI:         resourceProvisionTool,
		Name:        "onboarding provision_initial tool",
		Description: "Details for onboarding.v1.user_agents.provision_initial.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		body := "## onboarding.v1.user_agents.provision_initial\n\n" +
			"Input:\n" +
			"- `owner_matrix_user_id`\n" +
			"- optional `bot_username`\n" +
			"- optional `bot_password`\n\n" +
			"Normal use: pass only `owner_matrix_user_id` when the human says they want a new agent. If `bot_username` or `bot_password` are omitted, the provisioner generates them.\n\n" +
			"Output:\n" +
			"- `created`\n" +
			"- `already_exists`\n" +
			"- `scenario_id`\n" +
			"- `bot_user_id`\n" +
			"- `bot_username`\n" +
			"- `bot_password` when a new account was created or resumed to completion\n"
		return textResource(req.Params.URI, body), nil
	})
}

func textResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     text,
		}},
	}
}

func (s *Server) String() string {
	return fmt.Sprintf("mcpserver(%p)", s.server)
}
