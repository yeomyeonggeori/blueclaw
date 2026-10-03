package acpsession

import "testing"

func TestARootMessageIsNotInAThreadBecauseItsSessionOpenedInOne(t *testing.T) {
	sessionOpenedInAThread := Addressing{ConversationID: "direct-1", IsThread: true}

	if (MessageContext{MessageID: "message-root"}).isThread(sessionOpenedInAThread) {
		t.Fatal("a root message took the thread flag of the message that opened its session")
	}
	if !(MessageContext{MessageID: "message-reply", IsThread: true}).isThread(Addressing{ConversationID: "direct-1"}) {
		t.Fatal("a reply in a thread lost its own thread flag")
	}
	if !(MessageContext{}).isThread(sessionOpenedInAThread) {
		t.Fatal("a turn that carries no message lost the session's thread flag")
	}
}
