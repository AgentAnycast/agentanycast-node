package anycast

import (
	"log/slog"
	"testing"
)

func TestSkillCID_Deterministic(t *testing.T) {
	// Same skill ID always produces the same CID.
	cid1 := skillCID("translate")
	cid2 := skillCID("translate")
	if cid1 != cid2 {
		t.Fatalf("skillCID not deterministic: %s != %s", cid1, cid2)
	}
}

func TestSkillCID_DifferentSkills(t *testing.T) {
	// Different skill IDs produce different CIDs.
	cid1 := skillCID("translate")
	cid2 := skillCID("summarize")
	if cid1 == cid2 {
		t.Fatalf("skillCID collision: translate and summarize produced same CID: %s", cid1)
	}
}

func TestSkillCID_NonEmpty(t *testing.T) {
	c := skillCID("translate")
	if !c.Defined() {
		t.Fatal("skillCID returned an undefined CID")
	}
}

func TestMetaKey_Format(t *testing.T) {
	key := metaKey("12D3KooWPeer", "translate")
	expected := "/agentanycast/meta/12D3KooWPeer/translate"
	if key != expected {
		t.Fatalf("metaKey: got %q, want %q", key, expected)
	}
}

func TestMetaKey_DifferentInputs(t *testing.T) {
	k1 := metaKey("peerA", "skill1")
	k2 := metaKey("peerA", "skill2")
	k3 := metaKey("peerB", "skill1")
	if k1 == k2 {
		t.Fatal("metaKey: same peer different skill should differ")
	}
	if k1 == k3 {
		t.Fatal("metaKey: different peer same skill should differ")
	}
}

func TestDHTDiscovery_UnregisterSkills_Selective(t *testing.T) {
	// Test the pure logic of UnregisterSkills without a real DHT.
	// We construct a DHTDiscovery with only the localSkills map populated.
	dd := &DHTDiscovery{
		logger: slog.Default(),
		localSkills: map[string][]SkillInfo{
			"peer1": {
				{SkillID: "translate", Description: "Translates text"},
				{SkillID: "summarize", Description: "Summarizes text"},
				{SkillID: "classify", Description: "Classifies text"},
			},
		},
		localMeta: map[string]agentMeta{
			"peer1": {agentName: "TestAgent"},
		},
		done: make(chan struct{}),
	}

	// Remove "summarize" selectively.
	_ = dd.UnregisterSkills(nil, "peer1", []string{"summarize"})

	remaining := dd.localSkills["peer1"]
	if len(remaining) != 2 {
		t.Fatalf("expected 2 skills remaining, got %d", len(remaining))
	}
	for _, s := range remaining {
		if s.SkillID == "summarize" {
			t.Fatal("summarize should have been removed")
		}
	}
}

func TestDHTDiscovery_UnregisterSkills_All(t *testing.T) {
	dd := &DHTDiscovery{
		logger: slog.Default(),
		localSkills: map[string][]SkillInfo{
			"peer1": {{SkillID: "translate"}},
		},
		localMeta: map[string]agentMeta{},
		done:      make(chan struct{}),
	}

	// Empty skillIDs means remove all skills for this peer.
	_ = dd.UnregisterSkills(nil, "peer1", []string{})

	if _, exists := dd.localSkills["peer1"]; exists {
		t.Fatal("expected peer1 entry to be deleted when unregistering all skills")
	}
}
