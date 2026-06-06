package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/defomok-max/Ties/internal/agent"
)

// ralphDoneMarker is the sentinel the agent prints (on its own line) to signal
// that an autonomous loop has fully achieved its goal. Named after the "Ralph
// Wiggum" loop technique of re-running an agent until it declares completion.
const ralphDoneMarker = "TIES_TASK_COMPLETE"

const ralphNote = "\n\nAUTONOMOUS LOOP MODE: You are running in a bounded, repeating loop. " +
	"Each iteration, make concrete progress toward the goal, then verify your own " +
	"work (build, run tests, re-read changed files). When — and only when — the goal " +
	"is fully achieved AND verified, reply with " + ralphDoneMarker + " on its own line " +
	"and nothing else. If work remains, end your turn with a short status of what is " +
	"left; you will be asked to continue."

const tddModeNote = "\n\nTDD MODE: Follow strict test-driven development. " +
	"1) Write a failing test that captures the desired behavior. " +
	"2) Run it and confirm it fails for the right reason (red). " +
	"3) Write the minimum code to make it pass. " +
	"4) Run the tests and confirm they pass (green). " +
	"5) Refactor while keeping tests green. " +
	"Never write implementation before a failing test exists."

// runRalph drives the agent in an autonomous loop until it prints the done
// marker, an optional --until phrase appears, or the iteration cap is reached.
// The shared session/transcript means each iteration sees prior progress.
func (a *app) runRalph(ctx context.Context, ag *agent.Agent, input string, flags agentFlags) error {
	max := flags.maxLoops
	if max <= 0 {
		max = 12
	}
	for i := 0; i < max; i++ {
		prompt := input
		if i > 0 {
			prompt = "Continue working toward the original goal. Re-check and verify your work. " +
				"If the goal is fully achieved and verified, reply with " + ralphDoneMarker +
				" on its own line and nothing else."
		}
		if err := ag.Run(ctx, prompt); err != nil {
			return err
		}
		final := strings.TrimSpace(a.lastAssistant.String())
		if strings.Contains(final, ralphDoneMarker) {
			a.ui.Println(a.ui.Success(fmt.Sprintf("· loop complete after %d iteration(s)", i+1)))
			return nil
		}
		if flags.until != "" && strings.Contains(strings.ToLower(final), strings.ToLower(flags.until)) {
			a.ui.Println(a.ui.Success(fmt.Sprintf("· loop stop condition met after %d iteration(s)", i+1)))
			return nil
		}
		a.ui.Println(a.ui.Dim(fmt.Sprintf("· loop iteration %d/%d", i+1, max)))
	}
	a.ui.Println(a.ui.Warn(fmt.Sprintf("· loop reached max iterations (%d) without completion", max)))
	return nil
}
