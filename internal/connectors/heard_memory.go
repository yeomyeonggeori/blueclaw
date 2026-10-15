package connectors

import (
	"context"
	"log/slog"
	"time"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/memory"
	"github.com/yeomyeonggeori/bluememo"
)

const (
	heardMemoryTimeout      = 2 * time.Minute
	rememberedReactionEmoji = "memo"
)

type heardLayer struct {
	offer inboundengagement.MemoryLayer
	scope memory.Scope
}

// UseHeardMemory lets the gateway keep what a conversation states in a memory
// read by exactly the people in it. Without it no layer is offered.
func (connectorRuntime *ConnectorRuntime) UseHeardMemory(stores *memory.Stores) {
	connectorRuntime.heardMemory = stores
}

func (connectorRuntime *ConnectorRuntime) memoryLayersHeardIn(event PlatformInboundEvent) []heardLayer {
	if connectorRuntime.heardMemory == nil || connectorRuntime.identityService == nil {
		return nil
	}
	roomPersonIDs, isEveryoneKnown := connectorRuntime.personIDsOfRoomMembers(event.Context.RoomMemberEmails)
	if !isEveryoneKnown {
		return nil
	}
	scopes := memory.LayersReadByExactly(roomPersonIDs, connectorRuntime.identityService.EveryPersonAccess(), connectorRuntime.identityService.ContainedCircles())
	layers := make([]heardLayer, 0, len(scopes))
	for _, scope := range scopes {
		layers = append(layers, heardLayer{offer: memoryLayerOffer(scope, event), scope: scope})
	}
	return layers
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

func memoryLayerOffer(scope memory.Scope, event PlatformInboundEvent) inboundengagement.MemoryLayer {
	switch scope.Kind {
	case memory.ScopeWorkspace:
		return inboundengagement.MemoryLayer{Key: "company", Kind: inboundengagement.MemoryLayerCompany}
	case memory.ScopeCircle:
		return inboundengagement.MemoryLayer{Key: "circle:" + scope.ID, Kind: inboundengagement.MemoryLayerCircle, Name: scope.ID}
	}
	return inboundengagement.MemoryLayer{Key: "person", Kind: inboundengagement.MemoryLayerPerson, Name: event.Context.Sender.Name}
}

func memoryLayerOffers(layers []heardLayer) []inboundengagement.MemoryLayer {
	offers := make([]inboundengagement.MemoryLayer, 0, len(layers))
	for _, layer := range layers {
		offers = append(offers, layer.offer)
	}
	return offers
}

// rememberHeard keeps the message in the layer the gateway chose. The layer is
// looked up again rather than trusted from the decision, so a room whose
// members changed since cannot write where it no longer may. Settling asks a
// model, so it runs off the turn; a message kept for nothing else is marked
// once it is kept, so nobody in the room is left unaware of what was written.
func (connectorRuntime *ConnectorRuntime) rememberHeard(ctx context.Context, turn *inboundTurn, layerKey string, isAcknowledged bool) {
	layer, isOffered := heardLayerByKey(connectorRuntime.memoryLayersHeardIn(turn.event), layerKey)
	if !isOffered {
		connectorRuntime.logger.Warn("connector."+turn.platform+".memory.layer_closed", slog.String("messageID", turn.event.MessageID), slog.String("layer", layerKey))
		return
	}
	stack := memory.StackToRemember(layer.scope, memory.ScopesToSearch(turn.personAccess, connectorRuntime.identityService.ContainedCircles()))
	note := bluememo.Note{GroupID: memory.NewIdentifier(), Body: turn.event.Prompt, SpeakerName: turn.event.Context.Sender.Name}
	connectorRuntime.heardSettling.Go(func() {
		connectorRuntime.settleHeard(context.WithoutCancel(ctx), turn, layer, stack, note, isAcknowledged)
	})
}

func (connectorRuntime *ConnectorRuntime) settleHeard(ctx context.Context, turn *inboundTurn, layer heardLayer, stack []memory.Scope, note bluememo.Note, isAcknowledged bool) {
	ctx, cancel := context.WithTimeout(ctx, heardMemoryTimeout)
	defer cancel()
	report, errorValue := connectorRuntime.heardMemory.Remember(ctx, stack, note)
	if errorValue != nil {
		connectorRuntime.logger.Warn("connector."+turn.platform+".memory.remember_failed", slog.String("messageID", turn.event.MessageID), slog.String("scope", layer.scope.Kind), slog.String("scopeID", layer.scope.ID), slog.String("error", errorValue.Error()))
		return
	}
	connectorRuntime.logger.Info("connector."+turn.platform+".memory.remembered",
		slog.String("messageID", turn.event.MessageID),
		slog.String("scope", layer.scope.Kind),
		slog.String("scopeID", layer.scope.ID),
		slog.Int("inserted", report.Inserted),
		slog.Int("extended", report.Extended),
		slog.Int("superseded", report.Superseded),
		slog.Int("reinforced", report.Reinforced),
	)
	if isAcknowledged {
		connectorRuntime.addAddressingReaction(ctx, turn.platform, turn.adapter, turn.event, rememberedReactionEmoji)
	}
}

func heardLayerByKey(layers []heardLayer, key string) (heardLayer, bool) {
	for _, layer := range layers {
		if layer.offer.Key == key {
			return layer, true
		}
	}
	return heardLayer{}, false
}
