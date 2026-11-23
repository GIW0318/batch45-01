package task

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	. "gsb/batch45-safety-b-10"
)

func newMountGuard(root string) *MountTransactionGuard {
	return NewMountTransactionGuard(GuardOptions{
		MaxMounts:          4,
		AllowedPropagation: []string{"private", "slave"},
		Root:               root,
	})
}

func spec(source, target string) MountSpec {
	return MountSpec{Source: source, Target: target, FSType: "bind", Propagation: "private"}
}

func mustContainer(t *testing.T, g *MountTransactionGuard, id string, specs []MountSpec) *Transaction {
	t.Helper()
	tx, err := g.CreateContainer(id, specs)
	if err != nil {
		t.Fatalf("CreateContainer returned error: %v", err)
	}
	return tx
}

func TestApplyRollsBackOnPartialFailure(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{
		spec("/srv/a", "/mnt/a"),
		spec("/srv/b", "/mnt/b"),
	})
	mounts := 0
	ops, err := g.Apply(tx, func(op Op) error {
		if op.Kind == "mount" {
			mounts++
			if mounts == 2 {
				return errors.New("mount failed")
			}
		}
		return nil
	})
	if err == nil {
		t.Fatalf("Apply must report the failing mount")
	}
	if len(g.MountsFor("c1")) != 0 {
		t.Fatalf("half applied mounts left behind: %+v", g.MountsFor("c1"))
	}
	rolledBack := false
	for _, op := range ops {
		if op.Kind == "unmount" && op.Target == "/mnt/a" {
			rolledBack = true
		}
	}
	if !rolledBack {
		t.Fatalf("operations %+v do not roll back the mount that succeeded", ops)
	}
}

func TestSharedPropagationRejected(t *testing.T) {
	g := newMountGuard("")
	bad := spec("/srv/a", "/mnt/a")
	bad.Propagation = "shared"
	if err := g.ValidateSpec(bad); err == nil {
		t.Fatalf("shared propagation must be rejected, it leaks mounts to the host")
	}
	if _, err := g.CreateContainer("c1", []MountSpec{bad}); err == nil {
		t.Fatalf("container with shared propagation was created")
	}
	ok := spec("/srv/a", "/mnt/a")
	if err := g.ValidateSpec(ok); err != nil {
		t.Fatalf("private propagation rejected: %v", err)
	}
}

func TestReadOnlyMountCannotBeRemountedRW(t *testing.T) {
	g := newMountGuard("")
	bad := spec("/srv/a", "/mnt/a")
	bad.ReadOnly = true
	bad.Options = []string{"rw"}
	if err := g.ValidateSpec(bad); err == nil {
		t.Fatalf("read only mount with rw option must be rejected")
	}
	good := spec("/srv/a", "/mnt/a")
	good.ReadOnly = true
	good.Options = []string{"ro"}
	if err := g.ValidateSpec(good); err != nil {
		t.Fatalf("read only mount with ro option rejected: %v", err)
	}
}

func TestConcurrentContainersIsolated(t *testing.T) {
	g := newMountGuard("")
	ro := spec("/srv/data", "/data")
	ro.ReadOnly = true
	rw := spec("/srv/data", "/data")
	txA := mustContainer(t, g, "a", []MountSpec{ro})
	txB := mustContainer(t, g, "b", []MountSpec{rw})
	var wg sync.WaitGroup
	for _, tx := range []*Transaction{txA, txB} {
		wg.Add(1)
		go func(tx *Transaction) {
			defer wg.Done()
			if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
				t.Errorf("Apply returned error: %v", err)
			}
		}(tx)
	}
	wg.Wait()
	aMounts := g.MountsFor("a")
	bMounts := g.MountsFor("b")
	if len(aMounts) != 1 || len(bMounts) != 1 {
		t.Fatalf("mounts leaked across containers: a=%d b=%d", len(aMounts), len(bMounts))
	}
	if !aMounts[0].Spec.ReadOnly || bMounts[0].Spec.ReadOnly {
		t.Fatalf("container options polluted each other: a=%+v b=%+v", aMounts[0].Spec, bMounts[0].Spec)
	}
}

func TestSymlinkSourceRejected(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "data")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	g := newMountGuard(root)
	if _, err := g.CreateContainer("c1", []MountSpec{spec(link, "/data")}); err == nil {
		t.Fatalf("source symlink resolving outside the root was accepted")
	}
	if _, err := g.CreateContainer("c2", []MountSpec{spec(inside, "/data")}); err != nil {
		t.Fatalf("source inside the root rejected: %v", err)
	}
}

func TestBusyUnmountErrorPropagated(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{spec("/srv/a", "/mnt/a")})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	_, err := g.Cleanup("c1", func(op Op) error {
		if op.Kind == "unmount" {
			return errors.New("target is busy")
		}
		return nil
	})
	if err == nil {
		t.Fatalf("busy unmount error was swallowed")
	}
	if len(g.MountsFor("c1")) != 1 {
		t.Fatalf("mount was dropped although unmounting failed")
	}
}

func TestCleanupAfterContainerExit(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{spec("/srv/a", "/mnt/a"), spec("/srv/b", "/mnt/b")})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	ops, err := g.Cleanup("c1", func(Op) error { return nil })
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("cleanup produced %d operations, want an unmount per mount", len(ops))
	}
	if len(g.MountsFor("c1")) != 0 {
		t.Fatalf("mounts survived container exit: %+v", g.MountsFor("c1"))
	}
}

func TestSameSourceDifferentOptionsIsolated(t *testing.T) {
	g := newMountGuard("")
	ro := spec("/srv/data", "/data")
	ro.ReadOnly = true
	rw := spec("/srv/data", "/data")
	txA := mustContainer(t, g, "a", []MountSpec{ro})
	txB := mustContainer(t, g, "b", []MountSpec{rw})
	if _, err := g.Apply(txA, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if _, err := g.Apply(txB, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if !g.MountsFor("a")[0].Spec.ReadOnly {
		t.Fatalf("container a lost its read only option")
	}
	if g.MountsFor("b")[0].Spec.ReadOnly {
		t.Fatalf("container b inherited container a's read only option")
	}
}

func TestUIDMapMismatchRejected(t *testing.T) {
	g := newMountGuard("")
	bad := spec("/srv/a", "/mnt/a")
	bad.UIDMap = 1000
	bad.HostUID = 0
	if err := g.ValidateSpec(bad); err == nil {
		t.Fatalf("mount whose owner uid does not match the uid map was accepted")
	}
	good := spec("/srv/a", "/mnt/a")
	good.UIDMap = 1000
	good.HostUID = 1000
	if err := g.ValidateSpec(good); err != nil {
		t.Fatalf("matching uid map rejected: %v", err)
	}
}

func TestMountLimitEnforced(t *testing.T) {
	g := newMountGuard("")
	specs := []MountSpec{
		spec("/srv/a", "/mnt/a"),
		spec("/srv/b", "/mnt/b"),
		spec("/srv/c", "/mnt/c"),
		spec("/srv/d", "/mnt/d"),
	}
	if _, err := g.CreateContainer("c1", specs); err != nil {
		t.Fatalf("four mounts rejected although MaxMounts is 4: %v", err)
	}
	specs = append(specs, spec("/srv/e", "/mnt/e"))
	if _, err := g.CreateContainer("c2", specs); err == nil {
		t.Fatalf("more mounts than MaxMounts was accepted")
	}
}

func TestSnapshotRestoreMounts(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{spec("/srv/a", "/mnt/a")})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	data, err := g.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	restored := newMountGuard("")
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	got := restored.MountsFor("c1")
	if len(got) != 1 || got[0].Spec.Target != "/mnt/a" {
		t.Fatalf("mounts lost after restart: %+v", got)
	}
}

func TestDuplicateTargetsRejected(t *testing.T) {
	g := newMountGuard("")
	if _, err := g.CreateContainer("c0", []MountSpec{spec("/srv/a", "/mnt/a"), spec("/srv/b", "/mnt/b")}); err != nil {
		t.Fatalf("distinct targets rejected: %v", err)
	}
	_, err := g.CreateContainer("c1", []MountSpec{spec("/srv/a", "/mnt/same"), spec("/srv/b", "/mnt/same")})
	if err == nil {
		t.Fatalf("two mounts on the same target were accepted")
	}
}

func TestPropagationDefaultsToPrivate(t *testing.T) {
	g := newMountGuard("")
	s := spec("/srv/a", "/mnt/a")
	s.Propagation = ""
	tx := mustContainer(t, g, "c1", []MountSpec{s})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got := g.PropagationFor("c1"); got != "private" {
		t.Fatalf("PropagationFor = %q, want private", got)
	}
}

func TestCleanupUnmountsInReverseOrder(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{spec("/srv/a", "/mnt/a"), spec("/srv/b", "/mnt/b")})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	var targets []string
	if _, err := g.Cleanup("c1", func(op Op) error {
		if op.Kind == "unmount" {
			targets = append(targets, op.Target)
		}
		return nil
	}); err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if len(targets) != 2 || targets[0] != "/mnt/b" || targets[1] != "/mnt/a" {
		t.Fatalf("unmount order = %v, want reverse mount order [/mnt/b /mnt/a]", targets)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	g := newMountGuard("")
	tx := mustContainer(t, g, "c1", []MountSpec{spec("/srv/a", "/mnt/a")})
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if _, err := g.Apply(tx, func(Op) error { return nil }); err != nil {
		t.Fatalf("second Apply returned error: %v", err)
	}
	if n := len(g.MountsFor("c1")); n != 1 {
		t.Fatalf("container holds %d mounts after two applies, want 1", n)
	}
}
