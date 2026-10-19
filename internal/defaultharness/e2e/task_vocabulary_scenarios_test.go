//go:build !nobundledharness

package e2e

import (
	"strings"
	"testing"
)

const virtualRequesterContext = `"context":{"requesterPersonID":"` + virtualRequesterPersonID + `"}`

func TestVirtualTaskVocabularyRefusesAMember(t *testing.T) {
	service := virtualCapabilityService{taskVocabulary: newVirtualTaskVocabulary(virtualRegisteredBusinesses)}
	response := service.response(virtualTaskVocabularyToolName, []byte(`{"input":{"businesses":[{"name":"`+virtualUnregisteredBusiness+`"}]},`+virtualRequesterContext+`}`))
	if !strings.Contains(response, virtualTaskVocabularyNotAdminError) || !strings.Contains(response, `"outcome":"denied"`) {
		t.Fatalf("expected the record's refusal for a member, got %s", response)
	}
	if strings.Contains(listedLabels(&service), virtualUnregisteredBusiness) {
		t.Fatal("a refused write changed the vocabulary")
	}
}

func TestVirtualTaskVocabularyTakesAnAdministratorsWholeList(t *testing.T) {
	service := virtualCapabilityService{
		adminPersonIDs: map[string]bool{virtualRequesterPersonID: true},
		taskVocabulary: newVirtualTaskVocabulary(virtualRegisteredBusinesses),
	}
	service.response("task_add", []byte(`{"input":{"title":"report"},`+virtualRequesterContext+`}`))
	refused := service.response("task_update", []byte(`{"input":{"taskHint":"task-1","business":"`+virtualUnregisteredBusiness+`"},`+virtualRequesterContext+`}`))
	if !strings.Contains(refused, virtualTaskLabelUnregisteredError) {
		t.Fatalf("expected an unregistered business to be refused, got %s", refused)
	}
	service.response(virtualTaskVocabularyToolName, []byte(`{"input":{"businesses":[{"name":"`+virtualUnregisteredBusiness+`"}]},`+virtualRequesterContext+`}`))
	if strings.Contains(listedLabels(&service), virtualRegisteredBusinesses[0]) {
		t.Fatal("expected the written list to replace the whole vocabulary")
	}
	updated := service.response("task_update", []byte(`{"input":{"taskHint":"task-1","business":"`+virtualUnregisteredBusiness+`"},`+virtualRequesterContext+`}`))
	if !strings.Contains(updated, `"business":"`+virtualUnregisteredBusiness+`"`) {
		t.Fatalf("expected the registered business to be written, got %s", updated)
	}
}

func listedLabels(service *virtualCapabilityService) string {
	return service.response("task_list", []byte(`{"input":{},"context":{}}`))
}
