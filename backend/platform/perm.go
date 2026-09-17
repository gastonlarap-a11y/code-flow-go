package platform

import "io/fs"

// DirPerm is the mode every directory CodeFlow creates is made with.
//
// 0750, not 0755: {base} holds the user's database, their clones, their work-item mirrors and
// their review history. None of it is anyone else's business on a shared machine, and nothing
// outside this application — or the CLIs it spawns as the same user — ever needs to read it.
//
// It only applies to directories this version creates. MkdirAll leaves an existing directory's
// mode alone, so an install upgraded from 2.7.x keeps whatever it had: tightening those would be a
// change to the user's filesystem that a port has no business making silently.
const DirPerm fs.FileMode = 0o750

// FilePerm is the mode for files CodeFlow creates itself — logs, the reset marker, scratch.
//
// 0640 for the same reason: errors.log and shell.log are redacted, but "redacted" is a
// best-effort filter over a remote host's error bodies, not a guarantee.
const FilePerm fs.FileMode = 0o640
