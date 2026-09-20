// Command inventory audits the port's test coverage against the C# suite it replaces.
//
// MIGRATION-GO.md §11 Phase 9 step 1: "`docs/verbatim/test-inventory.md` fully ticked, or each
// unported test recorded with its reason in the owning spec document." This answers the first half
// mechanically so the second half is a short list somebody can actually read.
//
//	go run ./tools/inventory            # the per-folder and per-class summary
//	go run ./tools/inventory -class Foo # every entry of one class, with its best Go match
//	go run ./tools/inventory -gaps      # only what looks unported, ready to triage
//
// # This tool locates. It does not certify.
//
// That distinction is the most important thing about it, and it was learned by building the other
// thing first and watching it lie.
//
// §9.2 of the plan asked the port to keep the C# method names as subtest names "so parity is
// grep-able". **The port did not do that**, and the evidence is unambiguous: only 16 of the 121 C#
// classes have a same-named Go test file, and inside them the sentences were rewritten. C# says
// `A_file_committed_then_edited_again_appears_once_with_its_cumulative_change`; Go says
// `TestBranchContributionCountsATwiceTouchedFileOnce`. Same behaviour, same file, two words in
// common. Any score computed from those two names measures how two people chose to phrase one idea
// in English, and nothing whatsoever about whether the idea is tested.
//
// So this tool does not report coverage, because it cannot. It reports two things that are true:
//
//   - **Volume per area** — how many behaviours each C# folder pinned against how many the Go
//     packages that replaced it pin. Not proof, but an area with a third of the assertions it used
//     to have is worth looking at, and one with twice as many probably is not.
//   - **Where a class went** — for each C# class, the Go file its entries most resemble. A locator
//     for a human reading the two side by side, which is what step 1 actually requires.
//
// Whether a behaviour is still pinned is answered by reading, in the owning spec document. This
// narrows the reading from 1 232 entries to the classes that look thin.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// goPackagesFor maps a C# test folder to the Go packages that replaced it.
//
// Curated rather than derived, because the port did not preserve the folder structure and should
// not have: `Ipc` became the bridge plus the command-coverage contract in `app`, and `TestVectors`
// became a shared loader every feature calls. A folder whose tests moved somewhere unlisted still
// gets found — the fallback below searches the whole tree and reports where — so a wrong entry here
// costs accuracy in the report, never a false gap.
var goPackagesFor = map[string][]string{
	"Activity":    {"backend/activity"},
	"Ai":          {"backend/ai"},
	"ApiClient":   {"backend/apiclient"},
	"Dbml":        {"backend/dbml"},
	"Diagnostics": {"backend/diagnostics"},
	"Files":       {"backend/files"},
	"Git":         {"backend/git"},
	"Ipc":         {"backend/bridge", "backend/bridge/jsonwire", "backend/app"},
	"Platform":    {"backend/platform"},
	"Providers":   {"backend/providers"},
	"Review":      {"backend/review"},
	"Security":    {"backend/security"},
	"Storage":     {"backend/storage"},
	"Terminal":    {"backend/terminal"},
	"TestVectors": {"backend/shared/testvectors"},
	"Tickets":     {"backend/tickets"},
	"Update":      {"backend/update"},
	"Workspaces":  {"backend/workspaces"},
}

// resembles is the score above which two names are taken to be about the same subject — enough to
// use one as a pointer to the other, nowhere near enough to call a behaviour covered.
const resembles = 0.35

type verdict struct {
	test  csharpTest
	best  goBehaviour
	score float64
	// elsewhere is true when the best match was found outside the folder's mapped packages, which
	// is information rather than a problem: it says where the behaviour moved to.
	elsewhere bool
}

func main() {
	inventoryPath := flag.String("inventory", "docs/verbatim/test-inventory.md", "the C# inventory to audit against")
	backend := flag.String("backend", "backend", "the Go tree to search")
	only := flag.String("class", "", "report every entry of one class")
	gaps := flag.Bool("gaps", false, "report only what looks unported")
	flag.Parse()

	if err := run(*inventoryPath, *backend, *only, *gaps); err != nil {
		fmt.Fprintln(os.Stderr, "inventory: "+err.Error())
		os.Exit(1)
	}
}

func run(inventoryPath, backend, only string, gaps bool) error {
	tests, err := readInventory(inventoryPath)
	if err != nil {
		return err
	}
	behaviours, err := collectGoBehaviours(backend)
	if err != nil {
		return err
	}
	if len(tests) == 0 || len(behaviours) == 0 {
		return fmt.Errorf("read %d C# tests and %d Go behaviours; one of the paths is wrong", len(tests), len(behaviours))
	}

	byPackage := map[string][]goBehaviour{}
	for _, behaviour := range behaviours {
		byPackage[behaviour.pkg] = append(byPackage[behaviour.pkg], behaviour)
	}

	// Both suites are one corpus: a word is distinctive because it is rare *in this program's
	// vocabulary*, and both sides are describing the same program.
	corpus := make([]map[string]bool, 0, len(tests)+len(behaviours))
	for _, test := range tests {
		corpus = append(corpus, signature(test.name))
	}
	for _, behaviour := range behaviours {
		corpus = append(corpus, behaviour.sig)
	}
	weight := newWeights(corpus)

	verdicts := make([]verdict, 0, len(tests))
	for _, test := range tests {
		verdicts = append(verdicts, judge(weight, test, byPackage, behaviours))
	}

	switch {
	case only != "":
		reportClass(verdicts, only)
	case gaps:
		reportGaps(verdicts)
	default:
		reportSummary(verdicts, byPackage, len(behaviours))
	}
	return nil
}

// judge finds the Go behaviour that best matches one C# test: first in the folder's own packages,
// and only then anywhere, so a local match always wins over a coincidental distant one.
func judge(weight weights, test csharpTest, byPackage map[string][]goBehaviour, all []goBehaviour) verdict {
	want := signature(test.name)

	best, score := bestIn(weight, want, mapped(test.folder, byPackage))
	// A local match wins unless it is poor and a distant one is clearly better. Without that
	// margin the folder mapping stops mattering: `An_untracked_file_is_part_of_what_the_branch_
	// contributes` was located in `backend/tickets` over the `backend/git` test that replaced it,
	// on a one-word lead.
	if score >= resembles {
		return verdict{test: test, best: best, score: score}
	}

	elsewhereBest, elsewhereScore := bestIn(weight, want, all)
	if elsewhereScore > score*1.5 && elsewhereScore >= resembles {
		return verdict{test: test, best: elsewhereBest, score: elsewhereScore, elsewhere: true}
	}
	return verdict{test: test, best: best, score: score}
}

func mapped(folder string, byPackage map[string][]goBehaviour) []goBehaviour {
	var out []goBehaviour
	for _, pkg := range goPackagesFor[folder] {
		out = append(out, byPackage[pkg]...)
	}
	return out
}

func bestIn(weight weights, want map[string]bool, candidates []goBehaviour) (goBehaviour, float64) {
	var (
		best  goBehaviour
		score float64
	)
	for _, candidate := range candidates {
		if s := weight.overlap(want, candidate.sig); s > score {
			best, score = candidate, s
		}
	}
	return best, score
}

// ---- the reports --------------------------------------------------------------------------------

// classReport is where one C# class appears to have gone.
type classReport struct {
	folder string
	class  string
	// count is how many behaviours the C# class pinned.
	count int
	// home is the Go file most of its entries resemble, and located how many of them point there.
	home     string
	located  int
	resolved int
}

func reportSummary(verdicts []verdict, byPackage map[string][]goBehaviour, goCount int) {
	classes := summarise(verdicts)

	fmt.Printf("%d C# behaviours · %d Go behaviours across the tree\n", len(verdicts), goCount)
	fmt.Printf("Volume, not coverage — see this command's doc comment for why that distinction is load-bearing.\n\n")

	perFolder := map[string]int{}
	for _, v := range verdicts {
		perFolder[v.test.folder]++
	}

	fmt.Printf("%-14s %6s %6s %7s   %s\n", "area", "C#", "Go", "ratio", "Go packages")
	fmt.Println(strings.Repeat("-", 78))

	csharpTotal, goTotal := 0, 0
	for _, folder := range sortedStrings(perFolder) {
		csharp := perFolder[folder]
		packages := goPackagesFor[folder]

		inArea := 0
		for _, pkg := range packages {
			inArea += len(byPackage[pkg])
		}
		csharpTotal += csharp
		goTotal += inArea

		fmt.Printf("%-14s %6d %6d %6.1fx   %s\n",
			folder, csharp, inArea, float64(inArea)/float64(csharp), strings.Join(shorten(packages), " "))
	}
	fmt.Println(strings.Repeat("-", 78))
	fmt.Printf("%-14s %6d %6d %6.1fx\n\n", "all", csharpTotal, goTotal, float64(goTotal)/float64(csharpTotal))

	// The list worth reading: classes whose entries resemble nothing in particular, which is where
	// a whole class may have been dropped rather than reworded.
	var thin []classReport
	for _, report := range classes {
		if report.resolved == 0 {
			thin = append(thin, report)
		}
	}
	if len(thin) == 0 {
		fmt.Println("every class has at least one entry that resembles a Go test")
		return
	}

	fmt.Printf("%d of %d classes where nothing resembles a Go test — read these against the tree:\n\n",
		len(thin), len(classes))
	for _, report := range thin {
		fmt.Printf("  %-42s %3d behaviours\n", report.folder+"/"+report.class, report.count)
	}
	fmt.Printf("\n`-class <name>` reads one · `-gaps` lists every entry that resembles nothing\n")
}

// summarise collapses the per-entry verdicts into one row per class.
func summarise(verdicts []verdict) []classReport {
	order := make([]string, 0, 128)
	byKey := map[string]*classReport{}
	homes := map[string]map[string]int{}

	for _, v := range verdicts {
		key := v.test.folder + "/" + v.test.class
		if byKey[key] == nil {
			byKey[key] = &classReport{folder: v.test.folder, class: v.test.class}
			homes[key] = map[string]int{}
			order = append(order, key)
		}
		report := byKey[key]
		report.count++
		if v.score >= resembles {
			report.resolved++
			homes[key][v.best.file]++
		}
	}

	out := make([]classReport, 0, len(order))
	for _, key := range order {
		report := byKey[key]
		for file, count := range homes[key] {
			if count > report.located {
				report.home, report.located = file, count
			}
		}
		out = append(out, *report)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].folder != out[j].folder {
			return out[i].folder < out[j].folder
		}
		return out[i].class < out[j].class
	})
	return out
}

func reportClass(verdicts []verdict, class string) {
	wanted := strings.ToLower(class)
	shown := 0

	for _, v := range verdicts {
		if !strings.Contains(strings.ToLower(v.test.class), wanted) {
			continue
		}
		shown++

		marker := " "
		if v.test.theory {
			marker = "T"
		}
		fmt.Printf("%s %-4.0f%% %s\n", marker, v.score*100, v.test.name)
		if v.score >= resembles {
			where := v.best.file
			if v.elsewhere {
				where += "   (outside " + v.test.folder + "'s packages)"
			}
			fmt.Printf("        ~ %s\n          %s\n", v.best.name, where)
		}
	}
	if shown == 0 {
		fmt.Printf("no class matching %q; names come from the inventory's ### headings\n", class)
	}
}

func reportGaps(verdicts []verdict) {
	gaps := make([]verdict, 0, len(verdicts))
	for _, v := range verdicts {
		if v.score < resembles {
			gaps = append(gaps, v)
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].test.folder != gaps[j].test.folder {
			return gaps[i].test.folder < gaps[j].test.folder
		}
		if gaps[i].test.class != gaps[j].test.class {
			return gaps[i].test.class < gaps[j].test.class
		}
		return gaps[i].test.name < gaps[j].test.name
	})

	fmt.Printf("%d of %d entries resemble no Go test closely enough to point at one.\n", len(gaps), len(verdicts))
	fmt.Printf("Most are the same behaviour reworded — read, do not count.\n")

	class := ""
	for _, v := range gaps {
		key := v.test.folder + "/" + v.test.class
		if key != class {
			class = key
			fmt.Printf("\n%s\n", key)
		}
		marker := " "
		if v.test.theory {
			marker = "T"
		}
		fmt.Printf("  %s %-4.0f%% %s\n", marker, v.score*100, v.test.name)
	}
}

// shorten drops the `backend/` every package shares, so the column stays readable.
func shorten(packages []string) []string {
	out := make([]string, 0, len(packages))
	for _, pkg := range packages {
		out = append(out, strings.TrimPrefix(pkg, "backend/"))
	}
	return out
}

func sortedStrings(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
