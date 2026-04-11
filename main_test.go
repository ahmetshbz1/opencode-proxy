package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunDispatchesAuthSubcommand(t *testing.T) {
	called := false
	oldRunAuth := runAuthCommand
	runAuthCommand = func(ctx context.Context, args []string) error {
		called = true
		return nil
	}
	defer func() { runAuthCommand = oldRunAuth }()

	exitCode := run(context.Background(), []string{"opencode-proxy", "auth"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if !called {
		t.Fatal("auth komutu çağrılmadı")
	}
}

func TestParseRunFlagsCanBeCalledTwiceWithoutFlagPanic(t *testing.T) {
	for i := 0; i < 2; i++ {
		configPath, err := parseRunFlags([]string{"opencode-proxy", "-config", "test-config.json"})
		if err != nil {
			t.Fatalf("çağrı %d için parseRunFlags hatası: %v", i, err)
		}
		if configPath != "test-config.json" {
			t.Fatalf("çağrı %d için configPath = %q, want test-config.json", i, configPath)
		}
	}
}
