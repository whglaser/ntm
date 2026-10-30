package ensemble

import "testing"

func TestEnsembleRegistry_Basics(t *testing.T) {
	catalog := testModeCatalog(t)
	presets := []EnsemblePreset{
		{
			Name:        "one",
			Description: "first",
			Modes:       []ModeRef{ModeRefFromID("deductive"), ModeRefFromID("abductive")},
			Tags:        []string{"alpha", "core"},
		},
		{
			Name:        "two",
			Description: "second",
			Modes:       []ModeRef{ModeRefFromID("deductive"), ModeRefFromID("practical")},
			Tags:        []string{"beta"},
		},
	}

	registry := NewEnsembleRegistry(presets, catalog)
	if len(registry.ensembles) != 2 {
		t.Fatalf("ensemble count = %d, want 2", len(registry.ensembles))
	}
	if registry.Get("one") == nil {
		t.Fatal("expected preset 'one' to exist")
	}
	if got := registry.List(); len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if tagged := registry.ListByTag("alpha"); len(tagged) != 1 {
		t.Fatalf("ListByTag(alpha) len = %d, want 1", len(tagged))
	}
}

func TestEnsembleNames_And_GetEmbeddedEnsemble(t *testing.T) {
	names := EnsembleNames()
	if len(names) == 0 {
		t.Fatal("expected embedded ensemble names")
	}
	if GetEmbeddedEnsemble("does-not-exist") != nil {
		t.Fatal("expected missing ensemble to return nil")
	}
}
