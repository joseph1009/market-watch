package config

import "testing"

func TestTriageIsOnByDefaultWithTheSmallModel(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("TRIAGE", "")
	t.Setenv("TRIAGE_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Triage {
		t.Error("triage is off without TRIAGE set")
	}
	if cfg.TriageModel != DefaultTriageModel {
		t.Errorf("TriageModel = %q, want %q", cfg.TriageModel, DefaultTriageModel)
	}
}

func TestTriageCanBeTurnedOff(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("TRIAGE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Triage {
		t.Error("TRIAGE=false left triage on")
	}
}

func TestTriageRejectsUnknownValues(t *testing.T) {
	withRequiredEnv(t)
	t.Setenv("TRIAGE", "maybe")

	if _, err := Load(); err == nil {
		t.Error("an unknown TRIAGE value was accepted")
	}
}
