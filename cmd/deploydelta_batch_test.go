package cmd

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// --- batch splitting -----------------------------------------------------

func TestSplitSha256SumBatches_RespectsByteBudget(t *testing.T) {
	uploads := make([]string, 50)
	for i := range uploads {
		uploads[i] = fmt.Sprintf("src/very/long/path/segment/file-%03d.php", i)
	}
	const budget = 512

	batches := splitSha256SumBatches("/srv/app", uploads, budget)
	if len(batches) <= 1 {
		t.Fatalf("got %d batch(es), want splitting under a %d-byte budget", len(batches), budget)
	}

	// Every batch must fit the budget, and every path must survive exactly once,
	// in the original order.
	var flat []string
	for i, batch := range batches {
		if len(batch) == 0 {
			t.Fatalf("batch %d is empty", i)
		}
		cmdLen := len(buildSha256SumCommand("/srv/app", batch))
		if cmdLen > budget {
			t.Errorf("batch %d command = %d bytes, want <= %d", i, cmdLen, budget)
		}
		flat = append(flat, batch...)
	}
	if !reflect.DeepEqual(flat, uploads) {
		t.Errorf("flattened batches differ from the input order/content")
	}
}

func TestSplitSha256SumBatches_NeverSplitsAPath(t *testing.T) {
	uploads := []string{"a.php", "src/path with spaces and 'quotes'.php", "b.php"}

	for _, budget := range []int{1, 64, 200, sha256SumBatchBudget} {
		batches := splitSha256SumBatches("/srv/app", uploads, budget)
		var flat []string
		seen := map[string]int{}
		for _, batch := range batches {
			for _, rel := range batch {
				flat = append(flat, rel)
				seen[rel]++
			}
		}
		if !reflect.DeepEqual(flat, uploads) {
			t.Errorf("budget %d: flattened = %v, want %v (path must not be split)", budget, flat, uploads)
		}
		for rel, n := range seen {
			if n != 1 {
				t.Errorf("budget %d: %q appears %d times", budget, rel, n)
			}
		}
	}
}

func TestSplitSha256SumBatches_PathLargerThanBudgetGetsOwnBatch(t *testing.T) {
	long := "docs/" + strings.Repeat("very-long-segment/", 20) + "file.md"
	batches := splitSha256SumBatches("/srv/app", []string{"a.php", long, "b.php"}, 64)

	if len(batches) != 3 {
		t.Fatalf("got %d batches (%v), want 3", len(batches), batches)
	}
	if !reflect.DeepEqual(batches[1], []string{long}) {
		t.Errorf("oversized path batch = %v, want the path alone", batches[1])
	}
}

func TestSplitSha256SumBatches_DeduplicatesAndSkipsEmpty(t *testing.T) {
	batches := splitSha256SumBatches("/srv/app", []string{"a.php", "  ", "a.php", "", "b.php"}, sha256SumBatchBudget)
	if len(batches) != 1 {
		t.Fatalf("got %d batches (%v), want 1", len(batches), batches)
	}
	if want := []string{"a.php", "b.php"}; !reflect.DeepEqual(batches[0], want) {
		t.Errorf("batch = %v, want %v", batches[0], want)
	}
}

func TestSplitSha256SumBatches_LargeListSplitsMany(t *testing.T) {
	uploads := make([]string, 1500)
	for i := range uploads {
		uploads[i] = fmt.Sprintf("webapp/src/module%02d/component%04d.tsx", i%40, i)
	}
	batches := splitSha256SumBatches("/srv/www/app", uploads, sha256SumBatchBudget)
	if len(batches) < 2 {
		t.Fatalf("got %d batch(es) for 1500 paths, want several", len(batches))
	}
	total := 0
	for i, batch := range batches {
		total += len(batch)
		if n := len(buildSha256SumCommand("/srv/www/app", batch)); n > sha256SumBatchBudget {
			t.Errorf("batch %d command = %d bytes, want <= %d", i, n, sha256SumBatchBudget)
		}
	}
	if total != len(uploads) {
		t.Errorf("batches cover %d paths, want %d", total, len(uploads))
	}
}

// --- multi-batch execution ------------------------------------------------

// fakeRunner records the commands it receives and answers from a per-path hash
// map, mimicking `sha256sum` output for the paths present in each command.
type fakeRunner struct {
	hashes map[string]string // remote path -> hash
	errs   map[int]error     // 1-based call index -> error
	calls  []string
}

func (f *fakeRunner) run(cmd string) (string, error) {
	f.calls = append(f.calls, cmd)
	if err, ok := f.errs[len(f.calls)]; ok {
		return "", err
	}
	var sb strings.Builder
	for remote, sum := range f.hashes {
		if strings.Contains(cmd, shellEscapePOSIX(remote)) {
			fmt.Fprintf(&sb, "%s  %s\n", sum, remote)
		}
	}
	return sb.String(), nil
}

func TestPlanDeltaWithRunner_MergesBatches(t *testing.T) {
	uploads := []string{"a.php", "b.php", "c.php"}
	same := strings.Repeat("aa", 32)
	other := strings.Repeat("bb", 32)
	remote := map[string]string{
		"/srv/app/a.php": same,
		"/srv/app/b.php": other,
		"/srv/app/c.php": same,
	}
	local := map[string]string{"a.php": same, "b.php": same, "c.php": same}

	r := &fakeRunner{hashes: remote}
	delta := planDeltaWithRunner(r.run, "/srv/app", uploads, local)
	if delta.Fallback {
		t.Fatalf("unexpected fallback: %+v", delta)
	}
	if want := []string{"b.php"}; !reflect.DeepEqual(delta.Send, want) {
		t.Errorf("send = %v, want %v", delta.Send, want)
	}
	if want := []string{"a.php", "c.php"}; !reflect.DeepEqual(delta.Skip, want) {
		t.Errorf("skip = %v, want %v", delta.Skip, want)
	}
	if len(r.calls) != 1 {
		t.Errorf("runner calls = %d, want 1 for a small list", len(r.calls))
	}
}

func TestPlanDeltaWithRunner_MultiBatchMergesResults(t *testing.T) {
	// Long paths + a tiny budget are modelled by many paths; assert that the
	// planner issues one call per batch and still merges every answer.
	const n = 1500
	uploads := make([]string, n)
	local := make(map[string]string, n)
	remote := make(map[string]string, n)
	same := strings.Repeat("cc", 32)
	diff := strings.Repeat("dd", 32)
	for i := 0; i < n; i++ {
		rel := fmt.Sprintf("webapp/src/module%02d/component%04d.tsx", i%40, i)
		uploads[i] = rel
		local[rel] = same
		if i%2 == 0 {
			remote["/srv/www/app/"+rel] = same
		} else {
			remote["/srv/www/app/"+rel] = diff
		}
	}

	r := &fakeRunner{hashes: remote}
	delta := planDeltaWithRunner(r.run, "/srv/www/app", uploads, local)

	if delta.Fallback {
		t.Fatalf("unexpected fallback for %d paths", n)
	}
	if len(r.calls) < 2 {
		t.Fatalf("runner calls = %d, want multiple batches for %d paths", len(r.calls), n)
	}
	if got, want := len(delta.Send)+len(delta.Skip), n; got != want {
		t.Errorf("classified %d paths, want %d", got, want)
	}
	if got, want := len(delta.Send), n/2; got != want {
		t.Errorf("send = %d, want %d", got, want)
	}
	if got, want := len(delta.Skip), n/2; got != want {
		t.Errorf("skip = %d, want %d", got, want)
	}
}

func TestPlanDeltaWithRunner_FallbackOnSecondBatchError(t *testing.T) {
	const n = 1500
	uploads := make([]string, n)
	for i := 0; i < n; i++ {
		uploads[i] = fmt.Sprintf("webapp/src/module%02d/component%04d.tsx", i%40, i)
	}
	r := &fakeRunner{hashes: map[string]string{}, errs: map[int]error{2: fmt.Errorf("Process exited with status 1")}}

	delta := planDeltaWithRunner(r.run, "/srv/www/app", uploads, map[string]string{})
	if !delta.Fallback {
		t.Fatal("want fallback when a later batch fails")
	}
	if !reflect.DeepEqual(delta.Send, uploads) {
		t.Errorf("send = %d paths, want all %d (blind push)", len(delta.Send), len(uploads))
	}
	if len(delta.Skip) != 0 {
		t.Errorf("skip = %v, want empty on fallback", delta.Skip)
	}
	if len(r.calls) != 2 {
		t.Errorf("runner calls = %d, want 2 (stop after the failing batch)", len(r.calls))
	}
}

func TestPlanDeltaWithRunner_Sha256SumUnavailable(t *testing.T) {
	uploads := []string{"a.php", "b.php"}
	r := &fakeRunner{}
	delta := planDeltaWithRunner(func(string) (string, error) {
		return "bash: line 1: sha256sum: command not found\n", nil
	}, "/srv/app", uploads, map[string]string{"a.php": strings.Repeat("ee", 32)})
	_ = r

	if !delta.Fallback {
		t.Fatal("want fallback when sha256sum is unavailable")
	}
	if !reflect.DeepEqual(delta.Send, uploads) {
		t.Errorf("send = %v, want all uploads", delta.Send)
	}
}

func TestPlanDeltaWithRunner_EmptyUploads(t *testing.T) {
	delta := planDeltaWithRunner(func(string) (string, error) {
		t.Fatal("runner must not be called for an empty upload list")
		return "", nil
	}, "/srv/app", nil, nil)
	if delta.Fallback || len(delta.Send) != 0 || len(delta.Skip) != 0 {
		t.Errorf("delta = %+v, want empty", delta)
	}
}
