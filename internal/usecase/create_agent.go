package usecase

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pilot322/tmux-coder/internal/binresolve"
	"github.com/pilot322/tmux-coder/internal/domain"
)

type CreateAgentInput struct {
	ProjectID   int
	SessionID   int
	Kind        string
	DisplayName *string
	TmuxPaneID  *string
	DaemonAddr  string
}

type CreateAgentResult struct {
	Agent               *domain.Agent
	Project             *domain.Project
	Session             *domain.Session
	MainSessionName     string
	MainTmuxSessionName string
}

type CreateAgent struct {
	agents   IAgentRepository
	projects IProjectRepository
	sessions ISessionRepository
	tmux     AgentTmuxGateway
	lock     StateLock
}

func NewCreateAgent(a IAgentRepository, p IProjectRepository, s ISessionRepository, tmux AgentTmuxGateway, l StateLock) *CreateAgent {
	return &CreateAgent{agents: a, projects: p, sessions: s, tmux: tmux, lock: l}
}

func (uc *CreateAgent) Execute(ctx context.Context, in CreateAgentInput) (CreateAgentResult, error) {
	if in.ProjectID == 0 {
		return CreateAgentResult{}, fmt.Errorf("%w: projectId is required", ErrValidation)
	}
	if in.SessionID == 0 {
		return CreateAgentResult{}, fmt.Errorf("%w: sessionId is required", ErrValidation)
	}
	if in.Kind == "" {
		return CreateAgentResult{}, fmt.Errorf("%w: kind is required", ErrValidation)
	}
	if !validAgentKind(in.Kind) {
		return CreateAgentResult{}, fmt.Errorf("%w: kind must be an executable name", ErrValidation)
	}

	var project *domain.Project
	var session *domain.Session
	var sessions []*domain.Session
	if err := uc.lock.WithRead(func() error {
		p, err := uc.projects.GetByID(ctx, in.ProjectID)
		if err != nil {
			return err
		}
		project = p
		s, err := uc.sessions.GetByID(ctx, in.SessionID)
		if err != nil {
			return err
		}
		session = s
		allSessions, err := uc.sessions.GetAll(ctx)
		if err != nil {
			return err
		}
		sessions = allSessions
		return nil
	}); err != nil {
		return CreateAgentResult{}, err
	}

	if session.ProjectID() != in.ProjectID {
		return CreateAgentResult{}, fmt.Errorf("%w: session does not belong to project", ErrValidation)
	}

	paneOwned := in.TmuxPaneID == nil
	paneID := ""
	if in.TmuxPaneID != nil {
		paneID = *in.TmuxPaneID
		if !validStablePaneID(paneID) {
			return CreateAgentResult{}, fmt.Errorf("%w: tmuxPaneId must be a stable tmux pane id like %%12", ErrValidation)
		}
		panes, err := uc.tmux.ListPanes(ctx, session.TmuxName())
		if err != nil {
			return CreateAgentResult{}, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if !containsString(panes, paneID) {
			return CreateAgentResult{}, fmt.Errorf("%w: tmuxPaneId does not belong to session", ErrValidation)
		}
	}

	displayName := ""
	if in.DisplayName != nil {
		displayName = *in.DisplayName
	}

	var agent *domain.Agent
	if err := uc.lock.WithWrite(func() error {
		a, err := uc.agents.Create(ctx, domain.NewAgent(
			0, in.ProjectID, in.SessionID,
			in.Kind, displayName, paneID,
			paneOwned, domain.AgentStarting,
		))
		if err != nil {
			return err
		}
		agent = a
		return nil
	}); err != nil {
		return CreateAgentResult{}, err
	}

	if paneOwned {
		env := agentEnvVars(agent, in.DaemonAddr)
		cmd, err := wrapperCommand(agent.ID(), in.Kind)
		if err != nil {
			_ = uc.lock.WithWrite(func() error {
				return uc.agents.Delete(ctx, agent.ID())
			})
			return CreateAgentResult{}, err
		}
		resultPaneID, err := uc.tmux.NewWindow(ctx, session.TmuxName(), project.FullPath(), cmd, env)
		if err != nil {
			_ = uc.lock.WithWrite(func() error {
				return uc.agents.Delete(ctx, agent.ID())
			})
			return CreateAgentResult{}, fmt.Errorf("%w: %v", ErrGateway, err)
		}
		if err := uc.lock.WithWrite(func() error {
			current, err := uc.agents.GetByID(ctx, agent.ID())
			if err != nil {
				return err
			}
			agent = current.WithTmuxPaneID(resultPaneID)
			_, err = uc.agents.Update(ctx, agent)
			return err
		}); err != nil {
			return CreateAgentResult{}, err
		}
	}

	res := CreateAgentResult{Agent: agent, Project: project, Session: session}
	for _, s := range sessions {
		if s.ProjectID() == project.ID() && s.Type() == domain.MainSession {
			res.MainSessionName = s.Name()
			res.MainTmuxSessionName = s.TmuxName()
			break
		}
	}
	return res, nil
}

func agentEnvVars(agent *domain.Agent, daemonAddr string) []string {
	return []string{
		fmt.Sprintf("TMUX_CODER_AGENT_ID=%d", agent.ID()),
		fmt.Sprintf("TMUX_CODER_AGENT_KIND=%s", agent.Kind()),
		fmt.Sprintf("TMUX_CODER_PROJECT_ID=%d", agent.ProjectID()),
		fmt.Sprintf("TMUX_CODER_SESSION_ID=%d", agent.SessionID()),
		fmt.Sprintf("TMUX_CODER_PANE_ID=%s", agent.TmuxPaneID()),
		fmt.Sprintf("TMUX_CODERD_ADDR=%s", daemonAddr),
	}
}

func wrapperCommand(agentID int, kind string) (string, error) {
	executable, _ := os.Executable()
	wrapper, err := binresolve.ResolveSiblingThenPath(executable, "tmux-coderd-wrapper", exec.LookPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%q %d %s", wrapper, agentID, kind), nil
}

func validStablePaneID(paneID string) bool {
	if len(paneID) < 2 || paneID[0] != '%' {
		return false
	}
	for _, r := range paneID[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validAgentKind(kind string) bool {
	if kind == "" || strings.HasPrefix(kind, "-") {
		return false
	}
	for _, r := range kind {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
