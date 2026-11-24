package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"Add 22 and 35."}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") || stdout.Len() != 0 {
		t.Fatal("missing API key was not handled clearly")
	}
}
