package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/pilot322/tmux-coder/internal/binresolve"
	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/obs"
)

type CreateAgentInput struct {
	ProjectID   int
	SessionID   int
	Kind        string
	Model       *string
	Prompt      *string
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
	log      obs.Logger
	setup    *OpenCodeSetupCoordinator
	process  AgentProcessGateway
}

func NewCreateAgentWithOpenCodeSetup(a IAgentRepository, p IProjectRepository, s ISessionRepository, tmux AgentTmuxGateway, process AgentProcessGateway, l StateLock, log obs.Logger, setup *OpenCodeSetupCoordinator) *CreateAgent {
	uc := NewCreateAgent(a, p, s, tmux, l, log, setup)
	uc.process = process
	return uc
}

func NewCreateAgent(a IAgentRepository, p IProjectRepository, s ISessionRepository, tmux AgentTmuxGateway, l StateLock, log obs.Logger, setup ...*OpenCodeSetupCoordinator) *CreateAgent {
	uc := &CreateAgent{agents: a, projects: p, sessions: s, tmux: tmux, lock: l, log: log.With("component", "create-agent")}
	if len(setup) > 0 {
		uc.setup = setup[0]
	}
	return uc
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
	if err := validateOpenCodeSetup(in.Kind, in.Model, in.Prompt); err != nil {
		return CreateAgentResult{}, err
	}
	setupRequested := in.Model != nil || in.Prompt != nil
	if setupRequested && uc.setup == nil {
		return CreateAgentResult{}, fmt.Errorf("%w: OpenCode startup setup is unavailable", ErrGateway)
	}

	var project *domain.Project
	var session *domain.Session
	var sessions []*domain.Session
	workingDir := ""
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
		workingDir = agentWorkingDir(project, session)
		if session.Type() == domain.SecondarySession {
			_, _, root, err := secondaryParentRoot(ctx, uc.sessions, uc.projects, session.Parent())
			if err != nil {
				return err
			}
			workingDir = filepath.Join(root, session.RelativeWorkingDirectory())
		}
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
		candidate := domain.NewAgent(
			0, in.ProjectID, in.SessionID,
			in.Kind, displayName, paneID,
			paneOwned, domain.AgentStarting,
		)
		if in.Model != nil {
			candidate = candidate.WithModel(*in.Model)
		}
		a, err := uc.agents.Create(ctx, candidate)
		if err != nil {
			return err
		}
		agent = a
		return nil
	}); err != nil {
		return CreateAgentResult{}, err
	}
	if setupRequested {
		uc.setup.Register(agent.ID(), agent.Model(), in.Prompt, paneID)
	}

	if paneOwned {
		env := agentEnvVars(agent, in.DaemonAddr, setupRequested)
		cmd, err := agentWrapperCommand(agent.ID(), in.Kind)
		if err != nil {
			if setupRequested {
				uc.setup.Cancel(agent.ID())
				uc.setup.Forget(agent.ID())
			}
			_ = uc.lock.WithWrite(func() error {
				return uc.agents.Delete(ctx, agent.ID())
			})
			return CreateAgentResult{}, err
		}
		resultPaneID, err := uc.tmux.NewWindow(ctx, session.TmuxName(), agent.DisplayName(), workingDir, cmd, env)
		if err != nil {
			uc.log.Error(ctx, "agent window create failed, deleting agent record", "agent_id", agent.ID(), "kind", in.Kind, "err", err.Error())
			_ = uc.lock.WithWrite(func() error {
				return uc.agents.Delete(ctx, agent.ID())
			})
			if setupRequested {
				uc.setup.Cancel(agent.ID())
				uc.setup.Forget(agent.ID())
			}
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
			if setupRequested {
				uc.setup.Cancel(agent.ID())
				killErr := uc.stopFailedOwnedAgent(agent.ID(), resultPaneID)
				uc.setup.Forget(agent.ID())
				return CreateAgentResult{}, errors.Join(err, killErr)
			}
			return CreateAgentResult{}, err
		}
		if setupRequested {
			uc.setup.SetPane(agent.ID(), resultPaneID)
			if err := uc.setup.Wait(ctx, agent.ID()); err != nil {
				uc.setup.Cancel(agent.ID())
				killErr := uc.stopFailedOwnedAgent(agent.ID(), resultPaneID)
				uc.setup.Forget(agent.ID())
				setupErr := fmt.Errorf("%w: OpenCode startup setup failed: %v", ErrGateway, err)
				return CreateAgentResult{}, errors.Join(setupErr, killErr)
			}
			uc.setup.Forget(agent.ID())
		}
	} else if err := uc.tmux.RenameWindow(ctx, agent.TmuxPaneID(), agent.DisplayName()); err != nil {
		uc.log.Warn(ctx, "agent window rename failed", "agent_id", agent.ID(), "pane_id", agent.TmuxPaneID(), "display_name", agent.DisplayName(), "err", err.Error())
	}

	uc.log.Info(ctx, "agent created", "agent_id", agent.ID(), "kind", in.Kind, "session_id", in.SessionID, "pane_owned", paneOwned)
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

func (uc *CreateAgent) stopFailedOwnedAgent(agentID int, paneID string) error {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
	killErr := uc.tmux.KillPane(cleanupCtx, paneID)
	cleanupCancel()
	if killErr != nil {
		var pgid int
		_ = uc.lock.WithRead(func() error {
			agent, err := uc.agents.GetByID(context.Background(), agentID)
			if err == nil {
				pgid = agent.ChildProcessGroupID()
			}
			return nil
		})
		if pgid == 0 || uc.process == nil {
			return killErr
		}
		processCtx, processCancel := context.WithTimeout(context.Background(), 4*time.Second)
		processErr := uc.process.TerminateProcessGroup(processCtx, pgid, 2*time.Second)
		processCancel()
		if processErr != nil {
			return errors.Join(killErr, processErr)
		}
	}
	return uc.lock.WithWrite(func() error { return uc.agents.Delete(context.Background(), agentID) })
}

func agentWorkingDir(project *domain.Project, session *domain.Session) string {
	switch session.Type() {
	case domain.WorktreeSession:
		if session.WorktreePath() != "" {
			return session.WorktreePath()
		}
	case domain.SecondarySession:
		if session.RelativeWorkingDirectory() != "" {
			return filepath.Join(project.FullPath(), session.RelativeWorkingDirectory())
		}
	}
	return project.FullPath()
}

func agentEnvVars(agent *domain.Agent, daemonAddr string, setup bool) []string {
	env := []string{
		fmt.Sprintf("TMUX_CODER_AGENT_ID=%d", agent.ID()),
		fmt.Sprintf("TMUX_CODER_AGENT_KIND=%s", agent.Kind()),
		fmt.Sprintf("TMUX_CODER_PROJECT_ID=%d", agent.ProjectID()),
		fmt.Sprintf("TMUX_CODER_SESSION_ID=%d", agent.SessionID()),
		fmt.Sprintf("TMUX_CODER_PANE_ID=%s", agent.TmuxPaneID()),
		fmt.Sprintf("TMUX_CODERD_ADDR=%s", daemonAddr),
	}
	if setup {
		env = append(env, "TMUX_CODER_AGENT_SETUP=1", "TMUX_CODER_AGENT_SETUP_OWNER=daemon")
	}
	if agent.Model() != "" {
		env = append(env, "TMUX_CODER_AGENT_MODEL="+agent.Model())
	}
	return env
}

func validateOpenCodeSetup(kind string, model, prompt *string) error {
	if (model != nil || prompt != nil) && kind != "opencode" {
		return fmt.Errorf("%w: model and prompt are only supported for the opencode Agent Kind", ErrValidation)
	}
	if model != nil && !validCanonicalModel(*model) {
		return fmt.Errorf("%w: model must use canonical provider/model format", ErrValidation)
	}
	if prompt != nil && *prompt == "" {
		return fmt.Errorf("%w: prompt must not be empty", ErrValidation)
	}
	return nil
}

func validCanonicalModel(model string) bool {
	provider, modelID, ok := strings.Cut(model, "/")
	return ok && provider != "" && modelID != "" && strings.IndexFunc(model, unicode.IsSpace) == -1
}

func agentWrapperCommand(agentID int, kind string) (string, error) {
	executable, _ := os.Executable()
	tmuxCoder, err := binresolve.ResolveSiblingThenPath(executable, "tmux-coder", exec.LookPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%q agent-wrapper %d %s", tmuxCoder, agentID, kind), nil
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
