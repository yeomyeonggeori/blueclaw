package modelstandin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"

	"github.com/yeomyeonggeori/blueclaw/agenttest"
)

type Ask struct {
	Kind             string   `json:"kind"`
	Model            string   `json:"model"`
	Authorization    string   `json:"authorization"`
	SchemaName       string   `json:"schemaName,omitempty"`
	OfferedToolNames []string `json:"offeredToolNames,omitempty"`
	QuestionNames    []string `json:"questionNames,omitempty"`
	About            string   `json:"about,omitempty"`
	AnsweredWith     string   `json:"answeredWith,omitempty"`
	Refusal          string   `json:"refusal,omitempty"`
}

const (
	AskKindCompletion = "completion"
	AskKindDecision   = "decision"
	AskKindEmbedding  = "embedding"
)

type Leftovers struct {
	Refusals   []string       `json:"refusals"`
	Unconsumed map[string]int `json:"unconsumed"`
}

const unconsumedTurnsName = "turn"

type Server struct {
	activityLog   io.Writer
	mutex         sync.Mutex
	languageModel *agenttest.ScriptedLanguageModel
	turns         []turnScript
	decidedTurn   *intaketest.Outcome
	asked         []Ask
	refusals      []string
}

func NewServer(activityLog io.Writer) *Server {
	return &Server{activityLog: activityLog, languageModel: newScriptedLanguageModel()}
}

func newScriptedLanguageModel() *agenttest.ScriptedLanguageModel {
	return agenttest.NewScriptedLanguageModel(agenttest.ScriptedLanguageModelOptions{
		ProviderName:             "stand-in",
		ModelName:                "stand-in",
		DefaultResponsesBySchema: map[string]string{memoryDecompositionSchemaName: noPropositions},
	})
}

const (
	memoryDecompositionSchemaName = "memory_decomposition"
	noPropositions                = `{"propositions":[]}`
)

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", server.serveCompletion)
	mux.HandleFunc("POST /v1/embeddings", server.serveEmbeddings)
	mux.HandleFunc("POST "+decisionsPath(), server.serveDecision)
	mux.HandleFunc("POST /script/turn", server.scriptTurn)
	mux.HandleFunc("POST /script/structured", server.scriptStructured)
	mux.HandleFunc("POST /script/action", server.scriptAction)
	mux.HandleFunc("POST /script/reset", server.reset)
	mux.HandleFunc("GET /asked", server.serveAsked)
	mux.HandleFunc("GET /leftovers", server.serveLeftovers)
	return mux
}

func decisionsPath() string {
	endpoint, errorValue := url.Parse(decisions.DefaultEndpointURL)
	if errorValue != nil {
		panic("decisions.DefaultEndpointURL is not a URL: " + errorValue.Error())
	}
	return endpoint.Path
}

func (server *Server) record(ask Ask) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if line, errorValue := json.Marshal(ask); errorValue == nil {
		fmt.Fprintln(server.activityLog, string(line))
	}
	server.asked = append(server.asked, ask)
	if ask.Refusal != "" {
		server.refusals = append(server.refusals, ask.Refusal)
	}
}

func (server *Server) refuse(writer http.ResponseWriter, ask Ask, reason string) {
	ask.Refusal = reason
	server.record(ask)
	http.Error(writer, "the model stand-in refused: "+reason, http.StatusUnprocessableEntity)
}

func (server *Server) reset(writer http.ResponseWriter, _ *http.Request) {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	server.languageModel = newScriptedLanguageModel()
	server.turns = nil
	server.decidedTurn = nil
	server.refusals = nil
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) scriptedLanguageModel() *agenttest.ScriptedLanguageModel {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return server.languageModel
}

func (server *Server) serveAsked(writer http.ResponseWriter, _ *http.Request) {
	server.mutex.Lock()
	asked := append([]Ask{}, server.asked...)
	server.mutex.Unlock()
	writeJSON(writer, asked)
}

func (server *Server) serveLeftovers(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, server.Leftovers())
}

func (server *Server) Leftovers() Leftovers {
	unconsumed := server.scriptedLanguageModel().PendingResponseCounts()
	server.mutex.Lock()
	defer server.mutex.Unlock()
	if len(server.turns) > 0 {
		unconsumed[unconsumedTurnsName] = len(server.turns)
	}
	return Leftovers{Refusals: append([]string{}, server.refusals...), Unconsumed: unconsumed}
}

func writeJSON(writer http.ResponseWriter, document any) {
	writer.Header().Set("Content-Type", "application/json")
	if errorValue := json.NewEncoder(writer).Encode(document); errorValue != nil {
		http.Error(writer, errorValue.Error(), http.StatusInternalServerError)
	}
}
