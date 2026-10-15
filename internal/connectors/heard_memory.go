package connectors

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/bluememo"
)

const (
	heardMemoryTimeout      = 2 * time.Minute
	rememberedReactionEmoji = "memo"
)

// UseHeardMemory lets the gateway keep what a conversation states in a memory
// read by exactly the people in it. Without it no layer is offered.
func (connectorRuntime *ConnectorRuntime) UseHeardMemory(stores *memory.Stores) {
	connectorRuntime.heardMemory = stores
}

func (connectorRuntime *ConnectorRuntime) memoryLayersHeardIn(event PlatformInboundEvent) []memory.Scope {
	if connectorRuntime.heardMemory == nil || connectorRuntime.identityService == nil {
		return nil
	}
	roomPersonIDs, isEveryoneKnown := connectorRuntime.personIDsOfRoomMembers(event.Context.RoomMemberEmails)
	if !isEveryoneKnown {
		return nil
	}
	return memory.LayersReadByExactly(roomPersonIDs, connectorRuntime.identityService.EveryPersonAccess(), connectorRuntime.identityService.ContainedCircles())
}

func (connectorRuntime *ConnectorRuntime) personIDsOfRoomMembers(emails []string) ([]string, bool) {
	if len(emails) == 0 {
		return nil, false
	}
	personIDs := make([]string, 0, len(emails))
	for _, email := range emails {
		personID, isFound := connectorRuntime.identityService.ResolvePersonIDByEmail(email)
		if !isFound {
			return nil, false
		}
		personIDs = append(personIDs, personID)
	}
	return personIDs, true
}

// rememberHeard keeps the message in the layer the gateway chose. The room is
// matched again rather than trusted from the decision, so a room whose members
// changed since cannot write where it no longer may. Settling asks a model, so
// it runs off the turn; a message kept for nothing else is marked once it is
// kept, so nobody in the room is left unaware of what was written.
func (connectorRuntime *ConnectorRuntime) rememberHeard(ctx context.Context, turn *inboundTurn) {
	layer := turn.addressingLaunch.RememberLayer
	if layer == (memory.Scope{}) {
		return
	}
	if !slices.Contains(connectorRuntime.memoryLayersHeardIn(turn.event), layer) {
		connectorRuntime.logger.Warn("connector."+turn.platform+".memory.layer_closed", slog.String("messageID", turn.event.MessageID), slog.String("scope", layer.Kind), slog.String("scopeID", layer.ID))
		return
	}
	stack := memory.StackToRemember(layer, memory.ScopesToSearch(turn.personAccess, connectorRuntime.identityService.ContainedCircles()))
	note := bluememo.Note{GroupID: memory.NewIdentifier(), Body: turn.event.Prompt, SpeakerName: turn.event.Context.Sender.Name}
	isAcknowledged := !turn.addressingLaunch.ShouldLaunch
	connectorRuntime.heardSettling.Go(func() {
		connectorRuntime.settleHeard(context.WithoutCancel(ctx), turn, stack, note, isAcknowledged)
	})
}

func (connectorRuntime *ConnectorRuntime) settleHeard(ctx context.Context, turn *inboundTurn, stack []memory.Scope, note bluememo.Note, isAcknowledged bool) {
	ctx, cancel := context.WithTimeout(ctx, heardMemoryTimeout)
	defer cancel()
	layer := stack[0]
	report, errorValue := connectorRuntime.heardMemory.Remember(ctx, stack, note)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".memory.remember_failed", slog.String("messageID", turn.event.MessageID), slog.String("scope", layer.Kind), slog.String("scopeID", layer.ID), slog.String("error", errorValue.Error()))
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".memory.remembered",
		slog.String("messageID", turn.event.MessageID),
		slog.String("scope", layer.Kind),
		slog.String("scopeID", layer.ID),
		slog.Int("inserted", report.Inserted),
		slog.Int("extended", report.Extended),
		slog.Int("superseded", report.Superseded),
		slog.Int("reinforced", report.Reinforced),
	)
	if isAcknowledged {
		connectorRuntime.addAddressingReaction(ctx, turn.platform, turn.adapter, turn.event, rememberedReactionEmoji)
	}
}
