package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

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
		ID:              res.Project.ID(),
		Title:           res.Project.Title(),
		FullPath:        res.Project.FullPath(),
		MainSessionName: res.MainSessionName,
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
			ID:              v.Project.ID(),
			Title:           v.Project.Title(),
			FullPath:        v.Project.FullPath(),
			MainSessionName: v.MainSessionName,
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
