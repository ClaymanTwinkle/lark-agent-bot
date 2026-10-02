package core

import (
	"strings"
)

// peerVersionMismatches returns the peers whose build differs from version
// (and from commit, when both sides know theirs: local builds share a
// version and differ only in commit). A peer without a version was
// registered by a build from before versions were recorded, which differs
// too.
func peerVersionMismatches(version, commit string, peers []RelayPeer) []RelayPeer {
	var out []RelayPeer
	for _, p := range peers {
		switch {
		case p.Version == "" || p.Version != version:
			out = append(out, p)
		case knownCommit(commit) && knownCommit(p.Commit) && p.Commit != commit:
			out = append(out, p)
		}
	}
	return out
}

func knownCommit(c string) bool {
	return c != "" && c != "none" && c != "unknown"
}

// peerVersionWarning tells the user that other bots on this machine run a
// build other than version/commit (pass commit "" to compare versions
// only), or returns "" when they all match or no peers are known.
func (e *Engine) peerVersionWarning(version, commit string) string {
	if e.relayManager == nil {
		return ""
	}
	mismatched := peerVersionMismatches(version, commit, e.relayManager.otherProcessPeers())
	if len(mismatched) == 0 {
		return ""
	}
	names := make([]string, 0, len(mismatched))
	for _, p := range mismatched {
		names = append(names, p.Project+" ("+peerBuildLabel(p, commit != "")+")")
	}
	self := version
	if knownCommit(commit) {
		self += ", " + commit
	}
	return e.i18n.Tf(MsgPeerVersionMismatch, self, strings.Join(names, ", "))
}

func peerBuildLabel(p RelayPeer, withCommit bool) string {
	label := p.Version
	if label == "" {
		label = "?"
	}
	if withCommit && knownCommit(p.Commit) {
		label += ", " + p.Commit
	}
	return label
}
