// Package domain holds tmux-coder's entities and pure business rules. It has
// no dependencies on other packages in this module.
package domain

// Project is a base directory managed by tmux-coder, identified by its
// absolute path on disk.
type Project struct {
	id       int
	fullPath string
}

// NewProject builds a Project. The caller assigns a unique id. fullPath is
// expected to be an existing absolute directory; validating that is the
// usecase's job, so this constructor does no I/O and cannot fail.
func NewProject(id int, fullPath string) *Project {
	return &Project{id: id, fullPath: fullPath}
}

func (p *Project) ID() int          { return p.id }
func (p *Project) FullPath() string { return p.fullPath }
