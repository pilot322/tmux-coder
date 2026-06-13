package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

type ProjectController struct {
	create *usecase.CreateProject
	list   *usecase.GetProjects
	delete *usecase.DeleteProject
}

func NewProjectController(c *usecase.CreateProject, l *usecase.GetProjects, d *usecase.DeleteProject) *ProjectController {
	return &ProjectController{create: c, list: l, delete: d}
}

// Create handles POST /projects: 201 when a new project is created, 200 when
// one already existed for the path.
func (pc *ProjectController) Create(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.FullPath == "" {
		writeError(w, http.StatusBadRequest, "fullPath is required")
		return
	}

	res, err := pc.create.Execute(r.Context(), usecase.CreateProjectInput{FullPath: req.FullPath, Title: req.Title})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}

	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, projectResponse{
		ID:                  res.Project.ID(),
		Title:               res.Project.Title(),
		FullPath:            res.Project.FullPath(),
		MainSessionName:     res.MainSessionName,
		MainTmuxSessionName: res.MainTmuxSessionName,
	})
}

// List handles GET /projects.
func (pc *ProjectController) List(w http.ResponseWriter, r *http.Request) {
	views, err := pc.list.Execute(r.Context())
	if err != nil {
		writeUsecaseError(w, err)
		return
	}

	resp := projectsResponse{Projects: make([]projectResponse, 0, len(views))}
	for _, v := range views {
		resp.Projects = append(resp.Projects, projectResponse{
			ID:                  v.Project.ID(),
			Title:               v.Project.Title(),
			FullPath:            v.Project.FullPath(),
			MainSessionName:     v.MainSessionName,
			MainTmuxSessionName: v.MainTmuxSessionName,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// Delete handles DELETE /projects/{id}: 204 on success, 404 for an unknown id.
func (pc *ProjectController) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an integer")
		return
	}
	if err := pc.delete.Execute(r.Context(), id); err != nil {
		writeUsecaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type SessionController struct {
	create *usecase.CreateSession
	list   *usecase.GetSessions
	delete *usecase.DeleteSession
}

func NewSessionController(c *usecase.CreateSession, l *usecase.GetSessions, d *usecase.DeleteSession) *SessionController {
	return &SessionController{create: c, list: l, delete: d}
}

type AgentController struct {
	create *usecase.CreateAgent
	list   *usecase.GetAgents
	event  *usecase.AgentEvent
	delete *usecase.DeleteAgent
}

type ResourceController struct {
	acquirePort *usecase.AcquirePort
}

func NewAgentController(c *usecase.CreateAgent, l *usecase.GetAgents, e *usecase.AgentEvent, d *usecase.DeleteAgent) *AgentController {
	return &AgentController{create: c, list: l, event: e, delete: d}
}

func NewResourceController(acquirePort *usecase.AcquirePort) *ResourceController {
	return &ResourceController{acquirePort: acquirePort}
}

func (sc *SessionController) List(w http.ResponseWriter, r *http.Request) {
	filter, err := parseSessionTypeFilter(r.URL.Query().Get("type"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var projectID *int
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "projectId must be an integer")
			return
		}
		projectID = &id
	}

	views, err := sc.list.Execute(r.Context(), usecase.GetSessionsInput{Type: filter, ProjectID: projectID})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	resp := sessionsResponse{Sessions: make([]sessionResponse, 0, len(views))}
	for _, v := range views {
		resp.Sessions = append(resp.Sessions, sessionDTO(v.Session, v.Project, v.MainSessionName, v.MainTmuxSessionName, v.Branch))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (sc *SessionController) Create(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	kind, err := parseSessionType(req.Type)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s, err := sc.create.Execute(r.Context(), usecase.CreateSessionInput{
		ProjectID:                req.ProjectID,
		Type:                     kind,
		Branch:                   req.Branch,
		Create:                   req.Create,
		BaseBranch:               req.BaseBranch,
		ParentSessionID:          req.ParentSessionID,
		RelativeWorkingDirectory: req.RelativeWorkingDirectory,
		PreferredName:            req.PreferredName,
		OnDelete:                 req.OnDelete,
	})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}

	projectID := req.ProjectID
	if projectID == 0 {
		projectID = s.ProjectID()
	}
	views, err := sc.list.Execute(r.Context(), usecase.GetSessionsInput{ProjectID: &projectID})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	for _, v := range views {
		if v.Session.ID() == s.ID() {
			writeJSON(w, http.StatusCreated, sessionDTO(v.Session, v.Project, v.MainSessionName, v.MainTmuxSessionName, v.Branch))
			return
		}
	}
	writeUsecaseError(w, usecase.ErrSessionNotFound)
}

func (sc *SessionController) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an integer")
		return
	}
	force, err := parseBoolQuery(r.URL.Query().Get("force"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := sc.delete.Execute(r.Context(), usecase.DeleteSessionInput{ID: id, Force: force}); err != nil {
		writeUsecaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ac *AgentController) Create(w http.ResponseWriter, r *http.Request) {
	var req createAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.ProjectID == 0 {
		writeError(w, http.StatusBadRequest, "projectId is required")
		return
	}
	if req.SessionID == 0 {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}

	daemonAddr := r.Host
	result, err := ac.create.Execute(r.Context(), usecase.CreateAgentInput{
		ProjectID:   req.ProjectID,
		SessionID:   req.SessionID,
		Kind:        req.Kind,
		DisplayName: req.DisplayName,
		TmuxPaneID:  req.TmuxPaneID,
		DaemonAddr:  daemonAddr,
	})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, agentViewToDTO(usecase.AgentView{
		Agent:               result.Agent,
		Project:             result.Project,
		Session:             result.Session,
		MainSessionName:     result.MainSessionName,
		MainTmuxSessionName: result.MainTmuxSessionName,
	}))
}

func (rc *ResourceController) AcquirePort(w http.ResponseWriter, r *http.Request) {
	var req acquirePortRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := rc.acquirePort.Execute(r.Context(), usecase.AcquirePortInput{
		Key:       req.Key,
		Start:     req.Start,
		End:       req.End,
		HookToken: req.HookToken,
		ProjectID: req.ProjectID,
		SessionID: req.SessionID,
	})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acquirePortResponse{Port: out.Port})
}

func (ac *AgentController) List(w http.ResponseWriter, r *http.Request) {
	var projectID, sessionID *int
	if raw := r.URL.Query().Get("projectId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "projectId must be an integer")
			return
		}
		projectID = &id
	}
	if raw := r.URL.Query().Get("sessionId"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "sessionId must be an integer")
			return
		}
		sessionID = &id
	}

	views, err := ac.list.Execute(r.Context(), usecase.GetAgentsInput{ProjectID: projectID, SessionID: sessionID})
	if err != nil {
		writeUsecaseError(w, err)
		return
	}

	resp := agentsResponse{Agents: make([]agentResponse, 0, len(views))}
	for _, v := range views {
		resp.Agents = append(resp.Agents, agentViewToDTO(v))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (ac *AgentController) Event(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an integer")
		return
	}
	var req agentEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Event == "" {
		writeError(w, http.StatusBadRequest, "event is required")
		return
	}

	if err := ac.event.Execute(r.Context(), usecase.AgentEventInput{AgentID: id, Event: req.Event, ChildProcessGroupID: req.ChildProcessGroupID}); err != nil {
		writeUsecaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ac *AgentController) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be an integer")
		return
	}
	if err := ac.delete.Execute(r.Context(), id); err != nil {
		writeUsecaseError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseSessionType(raw string) (domain.SessionType, error) {
	switch strings.ToLower(raw) {
	case "main":
		return domain.MainSession, nil
	case "secondary":
		return domain.SecondarySession, nil
	case "worktree":
		return domain.WorktreeSession, nil
	default:
		return 0, fmt.Errorf("type must be one of main, secondary, worktree")
	}
}

func parseSessionTypeFilter(raw string) (usecase.SessionTypeFilter, error) {
	if raw == "" {
		return usecase.AnySessionType, nil
	}
	switch strings.ToLower(raw) {
	case "main":
		return usecase.MainSessionType, nil
	case "secondary":
		return usecase.SecondarySessionType, nil
	case "worktree":
		return usecase.WorktreeSessionType, nil
	default:
		return 0, fmt.Errorf("type must be one of main, secondary, worktree")
	}
}

func parseBoolQuery(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("force must be true or false")
	}
}

func sessionDTO(s *domain.Session, p *domain.Project, mainSessionName, mainTmuxSessionName, branch string) sessionResponse {
	return sessionResponse{
		ID:                       s.ID(),
		Parent:                   s.Parent(),
		ParentSessionID:          s.Parent(),
		ProjectID:                s.ProjectID(),
		Name:                     s.Name(),
		SessionName:              s.Name(),
		TmuxName:                 s.TmuxName(),
		Type:                     sessionTypeString(s.Type()),
		Branch:                   branch,
		Worktree:                 s.WorktreePath(),
		RelativeWorkingDirectory: s.RelativeWorkingDirectory(),
		OnDelete:                 s.OnDelete(),
		Project: projectResponse{
			ID:                  p.ID(),
			Title:               p.Title(),
			FullPath:            p.FullPath(),
			MainSessionName:     mainSessionName,
			MainTmuxSessionName: mainTmuxSessionName,
		},
	}
}

func sessionTypeString(kind domain.SessionType) string {
	switch kind {
	case domain.MainSession:
		return "main"
	case domain.SecondarySession:
		return "secondary"
	case domain.WorktreeSession:
		return "worktree"
	default:
		return "unknown"
	}
}

func agentToDTO(a *domain.Agent) agentResponse {
	return agentResponse{
		ID:                  a.ID(),
		ProjectID:           a.ProjectID(),
		SessionID:           a.SessionID(),
		Kind:                a.Kind(),
		DisplayName:         a.DisplayName(),
		TmuxPaneID:          a.TmuxPaneID(),
		PaneOwned:           a.PaneOwned(),
		Status:              string(a.Status()),
		ChildProcessGroupID: a.ChildProcessGroupID(),
	}
}

func agentViewToDTO(v usecase.AgentView) agentResponse {
	return agentResponse{
		ID:                  v.Agent.ID(),
		ProjectID:           v.Agent.ProjectID(),
		SessionID:           v.Agent.SessionID(),
		Kind:                v.Agent.Kind(),
		DisplayName:         v.Agent.DisplayName(),
		TmuxPaneID:          v.Agent.TmuxPaneID(),
		PaneOwned:           v.Agent.PaneOwned(),
		Status:              string(v.Agent.Status()),
		ChildProcessGroupID: v.Agent.ChildProcessGroupID(),
		Project: projectResponse{
			ID:                  v.Project.ID(),
			Title:               v.Project.Title(),
			FullPath:            v.Project.FullPath(),
			MainSessionName:     v.MainSessionName,
			MainTmuxSessionName: v.MainTmuxSessionName,
		},
		Session: sessionResponse{
			ID:          v.Session.ID(),
			Parent:      v.Session.Parent(),
			ProjectID:   v.Session.ProjectID(),
			Name:        v.Session.Name(),
			SessionName: v.Session.Name(),
			TmuxName:    v.Session.TmuxName(),
			Type:        sessionTypeString(v.Session.Type()),
			Branch:      v.Session.Branch(),
			Worktree:    v.Session.WorktreePath(),
		},
	}
}
