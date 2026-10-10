package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultBranch != "" || cfg.Window.Days != 90 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestParseKeepsDefaultsForAbsentFields(t *testing.T) {
	cfg := Default()
	if err := Parse([]byte("default_branch: master\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultBranch != "master" {
		t.Errorf("default_branch = %q", cfg.DefaultBranch)
	}
	if len(cfg.PullRequests.Dependencies.Authors) == 0 {
		t.Errorf("dependency authors lost")
	}
}

func TestParseEmptyFile(t *testing.T) {
	cfg := Default()
	if err := Parse(nil, &cfg); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	cfg := Default()
	err := Parse([]byte("defualt_branch: master\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "defualt_branch") {
		t.Fatalf("err = %v, want unknown-field error naming the key", err)
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	cfg := Default()
	cfg.DefaultBranch = " main"
	cfg.PullRequests.Backport.BaseBranches = []string{"release-["}
	cfg.Window = Window{From: "2026-10-01", To: "2026-09-01"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"default_branch", "release-[", "after"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestWindowRange(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		w        Window
		from, to string
		wantErr  bool
	}{
		{name: "days ending today", w: Window{Days: 90}, from: "2026-07-11", to: "2026-10-08"},
		{name: "days ending at to", w: Window{Days: 7, To: "2026-09-30"}, from: "2026-09-24", to: "2026-09-30"},
		{name: "explicit", w: Window{From: "2026-07-10", To: "2026-10-08"}, from: "2026-07-10", to: "2026-10-08"},
		{name: "bad date", w: Window{From: "10/07/2026"}, wantErr: true},
		{name: "empty", w: Window{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to, err := tt.w.Range(now)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := from.Format(DateLayout); got != tt.from {
				t.Errorf("from = %s, want %s", got, tt.from)
			}
			if got := to.Format(DateLayout); got != tt.to {
				t.Errorf("to = %s, want %s", got, tt.to)
			}
		})
	}
}

func TestSizeValidation(t *testing.T) {
	limit := func(n float64) *float64 { return &n }
	tests := []struct {
		name    string
		size    func(*Size)
		wantErr string
	}{
		{"defaults", func(*Size) {}, ""},
		{"no buckets", func(s *Size) { s.Buckets = nil }, "must not be empty"},
		{"open bucket not last", func(s *Size) { s.Buckets = []Bucket{{Weight: 1}, {Weight: 2}} }, "only the last bucket"},
		{"last bucket capped", func(s *Size) { s.Buckets = []Bucket{{Max: limit(10), Weight: 1}} }, "must not have a max"},
		{"not ascending", func(s *Size) {
			s.Buckets = []Bucket{{Max: limit(50), Weight: 1}, {Max: limit(10), Weight: 2}, {Weight: 3}}
		}, "larger than"},
		{"zero weight", func(s *Size) { s.Buckets = []Bucket{{Weight: 0}} }, "positive"},
		{"bad glob", func(s *Size) { s.Exclude = []string{"gen["} }, "size.exclude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.size(&cfg.Size)
			err := cfg.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatal(err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseReplacesBuckets(t *testing.T) {
	cfg := Default()
	err := Parse([]byte("size:\n  buckets:\n    - {name: small, max: 100, weight: 1}\n    - {name: big, weight: 4}\n"), &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Size.Buckets) != 2 || cfg.Size.Buckets[1].Weight != 4 || cfg.Size.Buckets[1].Max != nil || len(cfg.Size.Exclude) == 0 {
		t.Errorf("size = %+v", cfg.Size)
	}
}

func TestComponentsValidation(t *testing.T) {
	tests := []struct {
		name    string
		m       []Component
		wantErr string
	}{
		{"valid", []Component{{Name: "UI", Paths: []string{"ui/"}}}, ""},
		{"no name", []Component{{Paths: []string{"ui/"}}}, "name must not be empty"},
		{"duplicate", []Component{{Name: "UI", Paths: []string{"ui/"}}, {Name: "UI", Paths: []string{"web/"}}}, "duplicate"},
		{"no paths", []Component{{Name: "UI"}}, "paths must not be empty"},
		{"bad glob", []Component{{Name: "UI", Paths: []string{"ui["}}}, "components.map[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			cfg.Components.Map = tt.m
			err := cfg.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatal(err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
