//go:build race

package files_test

// raceEnabled is true in a `-race` build, which is also the only build with `checkptr` turned on.
//
// It exists for one skip, in watcher_test.go, and the pair of files is the only way Go offers to
// ask the question: there is no `runtime.RaceEnabled`.
const raceEnabled = true
