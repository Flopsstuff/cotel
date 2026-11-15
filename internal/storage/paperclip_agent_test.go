package storage

import (
	"errors"
	"testing"
)

const agentA = "709c0db6-7e36-42ae-89df-ef06d7f1acad"

func TestPaperclipAgentMapping(t *testing.T) {
	db := openTestActorDB(t)
	wayland, err := db.CreateActor("Wayland")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateActor("Other")
	if err != nil {
		t.Fatal(err)
	}

	if name, err := db.ActorNameByPaperclipAgentID(agentA); err != nil || name != "" {
		t.Fatalf("unmapped agent resolved to %q (err %v)", name, err)
	}
	if err := db.SetActorPaperclipAgentID(wayland.ID, agentA); err != nil {
		t.Fatalf("set: %v", err)
	}
	if name, _ := db.ActorNameByPaperclipAgentID(agentA); name != "Wayland" {
		t.Fatalf("resolved to %q, want Wayland", name)
	}
	got, err := db.GetActorWithStats(wayland.ID, nil)
	if err != nil || got.PaperclipAgentID != agentA {
		t.Fatalf("GetActorWithStats.PaperclipAgentID = %q (err %v)", got.PaperclipAgentID, err)
	}
	list, _, err := db.ListActorsPage(ListActorsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range list {
		if a.Name == "Wayland" && a.PaperclipAgentID != agentA {
			t.Fatalf("list item PaperclipAgentID = %q", a.PaperclipAgentID)
		}
	}

	// Assigning the same agent to another actor moves it: one actor per agent.
	if err := db.SetActorPaperclipAgentID(other.ID, agentA); err != nil {
		t.Fatalf("move: %v", err)
	}
	if name, _ := db.ActorNameByPaperclipAgentID(agentA); name != "Other" {
		t.Fatalf("after move resolved to %q, want Other", name)
	}
	if n := queryInt64(t, db, `SELECT count(*) FROM actors WHERE paperclip_agent_id = ?`, agentA); n != 1 {
		t.Fatalf("%d actors hold the agent id, want 1", n)
	}

	// Clearing removes the mapping.
	if err := db.SetActorPaperclipAgentID(other.ID, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if name, _ := db.ActorNameByPaperclipAgentID(agentA); name != "" {
		t.Fatalf("after clear resolved to %q", name)
	}
}

func TestPaperclipAgentMapping_DeletedActor(t *testing.T) {
	db := openTestActorDB(t)
	a, err := db.CreateActor("Gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetActorPaperclipAgentID(a.ID, agentA); err != nil {
		t.Fatal(err)
	}
	if err := db.SoftDeleteActor(a.ID); err != nil {
		t.Fatal(err)
	}
	if name, _ := db.ActorNameByPaperclipAgentID(agentA); name != "" {
		t.Fatalf("soft-deleted actor still resolves: %q", name)
	}
	if err := db.SetActorPaperclipAgentID(a.ID, agentA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set on deleted actor: err = %v, want ErrNotFound", err)
	}
	if err := db.SetActorPaperclipAgentID("no-such-id", agentA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set on unknown actor: err = %v, want ErrNotFound", err)
	}
}
