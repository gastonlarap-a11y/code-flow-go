package providers

import (
	"context"
	"encoding/json"
)

// githubConnectionsKey is the app-setting the Settings screen writes: a JSON array with one entry
// per connected GitHub host. The tokens themselves live in the OS credential store, keyed per host.
const githubConnectionsKey = "github_connections"

// knownGitHubHosts is github.com plus every Enterprise host the user has connected (REVIEW-003).
//
// A malformed setting is tolerated down to the default rather than reported: it is written by the
// Settings screen and read by every detection path, and a review that refuses to start because a
// list of hosts did not parse would be a worse answer than not recognising an Enterprise remote.
func (d Deps) knownGitHubHosts(ctx context.Context) ([]string, error) {
	raw, err := d.Projects.GetSetting(ctx, githubConnectionsKey)
	if err != nil {
		// A failed read is a different thing from an unparseable value: the database is the
		// caller's problem, and swallowing it here would make every detection look like "not a
		// known host".
		return nil, err
	}
	return KnownGitHubHosts(hostsFromConnections(raw)), nil
}

// hostsFromConnections reads the host of every entry, ignoring everything else the entry carries
// (a username, whether it is the default) and anything that is not an object with a host.
func hostsFromConnections(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}

	var connections []struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal([]byte(*raw), &connections); err != nil {
		return nil
	}

	hosts := make([]string, 0, len(connections))
	for _, connection := range connections {
		if connection.Host != "" {
			hosts = append(hosts, connection.Host)
		}
	}
	return hosts
}
