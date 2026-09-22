package usage

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gastonlarap-a11y/code-flow/backend/shared/proc"
)

/*
Which plan the user is on (USAGE-005).

`claude auth status` answers JSON, and `subscriptionType` in it names the tier. That is the whole
reason the panel needs nothing from the user: the plan is discoverable, so the only thing left to
learn is the ceiling that goes with it, and that is learned by watching (see calibration.go).

It is a subprocess, so it obeys the same rules every other child obeys: it goes through
`shared/proc`, which cannot be handed a credential, and it is given a deadline — a login prompt that
blocks forever would otherwise hang the poll behind it.
*/

// planTimeout bounds the probe. The command is a local read of a config file; a second is generous,
// and anything slower is a CLI that is not going to answer.
const planTimeout = 5 * time.Second

// Plan is what a provider's CLI says about the account behind it.
type Plan struct {
	// Tier is the provider's own word for it: "pro", "max", "team". Empty when unknown.
	Tier string
	// LoggedIn is false when the CLI is installed but nobody has signed in.
	LoggedIn bool
}

// authStatus is the shape `claude auth status` prints. Named fields only: the same output carries
// the account's e-mail and organisation, and this package has no business keeping either.
type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	SubscriptionType string `json:"subscriptionType"`
}

// ClaudePlan asks the Claude CLI which subscription is signed in.
//
// Answers a zero Plan and no error when the CLI is missing or refuses — not being signed in is an
// ordinary state, and the panel shows the consumption it can still read from the transcripts.
func ClaudePlan(ctx context.Context, binary string) Plan {
	if strings.TrimSpace(binary) == "" {
		binary = "claude"
	}

	ctx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()

	// `proc.Command` already builds the environment that cannot carry a credential (SEC-007).
	out, err := proc.Command(ctx, binary, "auth", "status").Output()
	if err != nil {
		return Plan{}
	}

	var status authStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return Plan{}
	}

	return Plan{Tier: status.SubscriptionType, LoggedIn: status.LoggedIn}
}
