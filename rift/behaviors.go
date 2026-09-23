package rift

import "time"

// The behavior setters are shared by every builder that can carry a behaviors block, so an `is`
// response and a proxy response built with the same chain get the same block.

// behave applies set to the behaviors block at bp, creating it on first use, and returns self so
// the caller's chain keeps its concrete builder type.
func behave[B any](self B, bp **Behaviors, set func(*Behaviors)) B {
	if *bp == nil {
		*bp = &Behaviors{}
	}
	set(*bp)
	return self
}

func waitFor(d time.Duration) func(*Behaviors) {
	return func(b *Behaviors) { b.Wait = int(d.Milliseconds()) }
}

func waitBetween(minD, maxD time.Duration) func(*Behaviors) {
	return func(b *Behaviors) {
		b.Wait = map[string]JSON{"min": int(minD.Milliseconds()), "max": int(maxD.Milliseconds())}
	}
}

func repeatN(n int) func(*Behaviors) { return func(b *Behaviors) { b.Repeat = n } }

func decorateWith(js string) func(*Behaviors) { return func(b *Behaviors) { b.Decorate = js } }

func copySpec(spec JSON) func(*Behaviors) { return func(b *Behaviors) { b.Copy = spec } }

func lookupSpec(spec JSON) func(*Behaviors) { return func(b *Behaviors) { b.Lookup = spec } }

func shellTransformCmd(cmd []string) func(*Behaviors) {
	return func(b *Behaviors) {
		if len(cmd) == 1 {
			b.ShellTransform = cmd[0]
			return
		}
		vs := make([]JSON, len(cmd))
		for i, c := range cmd {
			vs[i] = c
		}
		b.ShellTransform = vs
	}
}
