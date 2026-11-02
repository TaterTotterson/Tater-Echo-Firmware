package main

import (
	"testing"

	"github.com/TaterTotterson/Tater-Echo-Firmware/internal/show"
)

func TestShouldReplaceConversationRejectsStaleSameTurnRevision(t *testing.T) {
	current := &show.Conversation{TurnID: "turn-1", Phase: "thinking", Revision: 8}
	stale := &show.Conversation{TurnID: "turn-1", Phase: "listening", Revision: 7}
	if shouldReplaceConversation(current, stale) {
		t.Fatal("stale listening revision should not replace newer thinking state")
	}
}

func TestShouldReplaceConversationAcceptsNewerAndNewTurn(t *testing.T) {
	current := &show.Conversation{TurnID: "turn-1", Phase: "thinking", Revision: 8}
	newer := &show.Conversation{TurnID: "turn-1", Phase: "replying", Revision: 9}
	if !shouldReplaceConversation(current, newer) {
		t.Fatal("newer same-turn revision should be accepted")
	}
	newTurn := &show.Conversation{TurnID: "turn-2", Phase: "listening", Revision: 1}
	if !shouldReplaceConversation(current, newTurn) {
		t.Fatal("new voice turn should be accepted even when its revision restarts")
	}
}
