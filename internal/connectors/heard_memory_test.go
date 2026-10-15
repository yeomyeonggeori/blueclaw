//go:build !nobundledharness

package connectors

import (
	"context"
	"slices"
	"testing"

	"github.com/yeomyeonggeori/blueclaw/internal/identity"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/blueclaw/internal/memory/memorytest"
	"github.com/yeomyeonggeori/blueclaw/internal/policy"
)

func companyWithLeadership() *identity.IdentityService {
	return identity.NewIdentityService(policy.PolicyProjection{
		PersonIDByEmail: map[string]string{
			"chief@example.com":    "person-chief",
			"director@example.com": "person-director",
			"engineer@example.com": "person-engineer",
		},
		PersonAccessByPersonID: map[string]policy.PersonAccess{
			"person-chief":    {PersonID: "person-chief", Circles: []string{"leadership"}},
			"person-director": {PersonID: "person-director", Circles: []string{"leadership"}},
			"person-engineer": {PersonID: "person-engineer"},
		},
	})
}

func heardMemoryRuntime(t *testing.T, rememberLayer string) (*ConnectorRuntime, *testAdapter, *memory.Stores, *scriptedGatewayDecider) {
	t.Helper()
	connectorRuntime, adapter, _ := newStubbedTestConnectorRuntime(t)
	connectorRuntime.identityService = companyWithLeadership()
	stores := memorytest.Open(t)
	connectorRuntime.UseHeardMemory(stores)
	decider := &scriptedGatewayDecider{addressing: inboundengagement.AddressingDecision{Target: inboundengagement.AddressingTargetAnyone, ReactionEmoji: "eyes"}, rememberLayer: rememberLayer}
	connectorRuntime.UseGatewayDecider(decider)
	return connectorRuntime, adapter, stores, decider
}

func roomEvent(roomMemberEmails ...string) PlatformInboundEvent {
	event := testChannelInboundEvent("message-heard")
	event.Prompt = "다음 분기부터 임원회의는 화요일 오전 9시입니다"
	event.Context.Sender.Name = "이샘플"
	event.Context.RoomMemberEmails = roomMemberEmails
	return event
}

func offeredKeys(facts inboundengagement.Facts) []string {
	keys := []string{}
	for _, layer := range facts.MemoryLayers {
		keys = append(keys, layer.Key)
	}
	return keys
}

func TestARoomOfExactlyTheLeadershipIsOfferedItsMemory(t *testing.T) {
	connectorRuntime, _, _, _ := heardMemoryRuntime(t, "")
	facts := connectorRuntime.gatewayFactsForTurn(context.Background(), connectorRuntime.OpenSessionTurn(context.Background(), roomEvent("chief@example.com", "director@example.com"), "person-chief", nil).turn)
	if keys := offeredKeys(facts); !slices.Equal(keys, []string{"circle:leadership"}) {
		t.Fatalf("expected the leadership layer alone, got %v", keys)
	}
}

func TestAnEmployeeInTheRoomKeepsTheLeadershipMemoryClosed(t *testing.T) {
	connectorRuntime, _, _, _ := heardMemoryRuntime(t, "")
	facts := connectorRuntime.gatewayFactsForTurn(context.Background(), connectorRuntime.OpenSessionTurn(context.Background(), roomEvent("chief@example.com", "director@example.com", "engineer@example.com"), "person-chief", nil).turn)
	if keys := offeredKeys(facts); slices.Contains(keys, "circle:leadership") {
		t.Fatalf("expected no leadership layer while an employee outside it is in the room, got %v", keys)
	}
}

func TestARoomWhoseMembersAreNotAllKnownIsOfferedNothing(t *testing.T) {
	connectorRuntime, _, _, _ := heardMemoryRuntime(t, "")
	facts := connectorRuntime.gatewayFactsForTurn(context.Background(), connectorRuntime.OpenSessionTurn(context.Background(), roomEvent("chief@example.com", "director@example.com", "guest@example.com"), "person-chief", nil).turn)
	if len(facts.MemoryLayers) != 0 {
		t.Fatalf("expected no layer for a room holding somebody the directory does not name, got %v", offeredKeys(facts))
	}
}

func TestAMessageWorthOnlyRememberingIsKeptAndMarkedWithAMemo(t *testing.T) {
	connectorRuntime, adapter, stores, _ := heardMemoryRuntime(t, "circle:leadership")
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), roomEvent("chief@example.com", "director@example.com"), "person-chief", nil)

	decision := sessionTurn.ResolveEngagement(context.Background())
	connectorRuntime.heardSettling.Wait()

	if decision.ShouldLaunch {
		t.Fatalf("expected nothing launched for a message only worth remembering, got %+v", decision)
	}
	if held := memorytest.Count(t, stores, memory.CircleScope("leadership")); held == 0 {
		t.Fatal("expected the leadership memory to hold what the room said")
	}
	if held := memorytest.Count(t, stores, memory.PersonScope("person-chief")); held != 0 {
		t.Fatalf("expected the speaker's own memory untouched, got %d", held)
	}
	emojiNames := []string{}
	for _, reaction := range adapter.reactions {
		emojiNames = append(emojiNames, reaction.EmojiName)
	}
	if !slices.Equal(emojiNames, []string{rememberedReactionEmoji}) {
		t.Fatalf("expected the memo alone to mark what was kept, got %v", emojiNames)
	}
}

func TestALayerTheRoomCannotWriteIsNeverWritten(t *testing.T) {
	connectorRuntime, adapter, stores, _ := heardMemoryRuntime(t, "circle:leadership")
	sessionTurn := connectorRuntime.OpenSessionTurn(context.Background(), roomEvent("chief@example.com", "director@example.com", "engineer@example.com"), "person-chief", nil)

	sessionTurn.ResolveEngagement(context.Background())
	connectorRuntime.heardSettling.Wait()

	if held := memorytest.Count(t, stores, memory.CircleScope("leadership")); held != 0 {
		t.Fatalf("expected the leadership memory untouched by a room it does not match, got %d", held)
	}
	if len(adapter.reactions) != 0 {
		t.Fatalf("expected no mark for what was not kept, got %+v", adapter.reactions)
	}
}
