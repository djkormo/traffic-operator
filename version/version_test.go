package version

import (
	"strings"
	"testing"
)

const defaultUnset = "unset"

func TestVersionVariables(t *testing.T) {
	if Application != "traffic-operator" {
		t.Errorf("expected Application to be 'traffic-operator', got %s", Application)
	}
	if BuildTime != defaultUnset {
		t.Errorf("expected default BuildTime to be 'unset', got %s", BuildTime)
	}
	if Commit != defaultUnset {
		t.Errorf("expected default Commit to be 'unset', got %s", Commit)
	}
	if Release != defaultUnset {
		t.Errorf("expected default Release to be 'unset', got %s", Release)
	}
}

func TestGet(t *testing.T) {
	info := Get()
	if info.Application != Application {
		t.Errorf("expected Application %s, got %s", Application, info.Application)
	}
	if info.BuildTime != BuildTime {
		t.Errorf("expected BuildTime %s, got %s", BuildTime, info.BuildTime)
	}
	if info.Commit != Commit {
		t.Errorf("expected Commit %s, got %s", Commit, info.Commit)
	}
	if info.Release != Release {
		t.Errorf("expected Release %s, got %s", Release, info.Release)
	}

	str := info.String()
	if !strings.Contains(str, Application) || !strings.Contains(str, Release) {
		t.Errorf("unexpected Info.String output: %s", str)
	}
}
