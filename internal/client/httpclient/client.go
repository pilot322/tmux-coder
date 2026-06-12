package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Project struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	FullPath        string `json:"fullPath"`
	MainSessionName string `json:"mainSessionName"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: hc}
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/projects", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Projects []Project `json:"projects"`
	}
	if err := c.doJSON(req, http.StatusOK, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

func (c *Client) CreateProject(ctx context.Context, fullPath string, title ...string) (Project, error) {
	var projectTitle *string
	if len(title) > 0 {
		projectTitle = &title[0]
	}
	body, err := json.Marshal(struct {
		FullPath string  `json:"fullPath"`
		Title    *string `json:"title,omitempty"`
	}{FullPath: fullPath, Title: projectTitle})
	if err != nil {
		return Project{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/projects", bytes.NewReader(body))
	if err != nil {
		return Project{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var project Project
	if err := c.doJSON(req, 0, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) DeleteProject(ctx context.Context, id int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/projects/%d", c.baseURL, id), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, http.StatusNoContent, nil)
}

func (c *Client) doJSON(req *http.Request, want int, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if want != 0 && resp.StatusCode != want || want == 0 && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return statusError(resp)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	return fmt.Errorf("%s", resp.Status)
}
