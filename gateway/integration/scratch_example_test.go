//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

func TestScratchServerClientSendAndReadExample(t *testing.T) {
	t.Parallel()
	native, gateway := aircdBinary(t), currentGatewayBinary(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "run", fmt.Sprintf("-race=%t", gatewayTestRace), "./examples/scratch", "--native", native, "--gateway", gateway)
	command.Dir = ".."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("scratch example failed: %v\n%s", err, output)
	}
	var result struct {
		Author      string `json:"author"`
		Target      string `json:"target"`
		Body        string `json:"body"`
		MessageID   string `json:"message_id"`
		FirstPage   int    `json:"first_page_messages"`
		ResumedPage int    `json:"resumed_page_messages"`
	}
	if err := json.Unmarshal(output, &result); err != nil || result.Author != "scratch-author" || result.Target != "#scratch" || result.Body != "scratch whole body\nhello 🙂" || result.MessageID == "" || result.FirstPage != 1 || result.ResumedPage != 0 {
		t.Fatalf("scratch example lost send/read evidence: %+v err=%v\n%s", result, err, output)
	}
}
