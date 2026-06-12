package domain_test

import (
	"errors"
	"testing"

	"github.com/pilot322/tmux-coder/internal/domain"
)

func TestCleanProjectTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		limit   int
		want    string
		wantErr bool
	}{
		{name: "trims", title: " Backend API ", limit: 40, want: "Backend API"},
		{name: "allows unicode", title: "José research", limit: 40, want: "José research"},
		{name: "allows punctuation", title: "api-service.v2", limit: 40, want: "api-service.v2"},
		{name: "rejects blank", title: "   ", limit: 40, wantErr: true},
		{name: "rejects adjacent spaces", title: "Backend  API", limit: 40, wantErr: true},
		{name: "rejects controls", title: "Backend\nAPI", limit: 40, wantErr: true},
		{name: "rejects over limit", title: "abcdef", limit: 5, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.CleanProjectTitle(tt.title, tt.limit)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidProjectTitle) {
					t.Fatalf("want ErrInvalidProjectTitle, got %v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("CleanProjectTitle = (%q, %v), want (%q, nil)", got, err, tt.want)
			}
		})
	}
}

func TestDefaultProjectTitleNormalizesUnsafeBasename(t *testing.T) {
	got := domain.DefaultProjectTitle(" Backend\n API  ", 40)
	if got != "Backend API" {
		t.Fatalf("DefaultProjectTitle = %q, want Backend API", got)
	}
}

func TestDefaultProjectTitleFallsBack(t *testing.T) {
	got := domain.DefaultProjectTitle("\n\t", 40)
	if got != "Project" {
		t.Fatalf("DefaultProjectTitle = %q, want Project", got)
	}
}
