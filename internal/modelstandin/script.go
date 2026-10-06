package modelstandin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement"
	"github.com/yeomyeonggeori/blueclaw/internal/inboundengagement/gatewaytest"
	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
)

type turnScript struct {
	Message string `json:"message"`
	intaketest.Outcome
	Addressing          inboundengagement.AddressingDecision `json:"addressing"`
	ReactionProbability float64                              `json:"reactionProbability"`
	RelatesToActiveTask bool                                 `json:"relatesToActiveTask"`
	BusyRoute           inboundengagement.BusyRoute          `json:"busyRoute"`
}

func (script turnScript) gatewayOutcome() gatewaytest.Outcome {
	return gatewaytest.Outcome{
		Addressing:          script.Addressing,
		ReactionProbability: script.ReactionProbability,
		BusyRoute:           script.BusyRoute,
		RelatesToActiveTask: script.RelatesToActiveTask,
	}
}

func (script turnScript) plansATurn() bool {
	return script.TurnDecision.Route != ""
}

type structuredScript struct {
	SchemaName string          `json:"schemaName"`
	Document   json.RawMessage `json:"document"`
}

type actionScript struct {
	ToolName  string          `json:"toolName"`
	Arguments json.RawMessage `json:"arguments"`
}

func (server *Server) scriptTurn(writer http.ResponseWriter, request *http.Request) {
	var script turnScript
	if errorValue := decodeStrictly(request, &script); errorValue != nil {
		http.Error(writer, "a turn is the message it decides and an intaketest.Outcome: "+errorValue.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(script.Message) == "" {
		http.Error(writer, "a turn names the message it decides", http.StatusBadRequest)
		return
	}
	server.mutex.Lock()
	server.turns = append(server.turns, script)
	server.mutex.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) scriptStructured(writer http.ResponseWriter, request *http.Request) {
	var script structuredScript
	if errorValue := decodeStrictly(request, &script); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	if errorValue := validateStructuredScript(script); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	server.scriptedLanguageModel().EnqueueStructuredResponses(script.SchemaName, string(script.Document))
	writer.WriteHeader(http.StatusNoContent)
}

func validateStructuredScript(script structuredScript) error {
	if strings.TrimSpace(script.SchemaName) == "" {
		return errors.New("a structured answer names the schema it answers")
	}
	if !isJSONObject(script.Document) {
		return errors.New("a structured answer is a JSON object")
	}
	return nil
}

func (server *Server) scriptAction(writer http.ResponseWriter, request *http.Request) {
	var script actionScript
	if errorValue := decodeStrictly(request, &script); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	actionDocument, errorValue := scriptedActionDocument(script)
	if errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusBadRequest)
		return
	}
	server.scriptedLanguageModel().EnqueueActionResponses(actionDocument)
	writer.WriteHeader(http.StatusNoContent)
}

func scriptedActionDocument(script actionScript) (string, error) {
	if strings.TrimSpace(script.ToolName) == "" {
		return "", errors.New("an agent action names the tool it calls")
	}
	if !isJSONObject(script.Arguments) {
		return "", errors.New("an agent action's arguments are a JSON object")
	}
	document, errorValue := json.Marshal(map[string]any{
		"action":    "continue",
		"toolName":  script.ToolName,
		"toolInput": script.Arguments,
	})
	return string(document), errorValue
}

func isJSONObject(document json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(document, &object) == nil && object != nil
}

func decodeStrictly(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if errorValue := decoder.Decode(target); errorValue != nil {
		return errorValue
	}
	if decoder.More() {
		return errors.New("the body carries more than one document")
	}
	return nil
}
