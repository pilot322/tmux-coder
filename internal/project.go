package main

import "context"

type Project struct {
	id       int
	fullPath string
}

// NewProject : creating a new project requires the path to already exist
func NewProject(id int, path string) (*Project, error) {
	return &Project{id, path}, nil
}

type IProjectRepository interface {
	Create(ctx context.Context, project *Project) error
	GetByID(ctx context.Context, id int) (*Project, error)
	GetAll(ctx context.Context) ([]*Project, error)
	Update(ctx context.Context, project *Project) error
	Delete(ctx context.Context, id int) error
}
