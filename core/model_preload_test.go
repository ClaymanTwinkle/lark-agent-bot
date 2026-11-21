package core

import (
	"context"
	"testing"
	"time"
)

type preloadingAgent struct {
	stubAgent
	preloaded chan context.Context
}

func (a *preloadingAgent) PreloadModels(ctx context.Context) { a.preloaded <- ctx }

func TestEngineStart_PreloadsModels(t *testing.T) {
	ag := &preloadingAgent{preloaded: make(chan context.Context, 1)}
	e := NewEngine("test", ag, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	if err := e.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop()
	select {
	case ctx := <-ag.preloaded:
		if ctx == nil {
			t.Fatal("PreloadModels got a nil context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not call PreloadModels")
	}
}
