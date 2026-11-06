package runtimecontrol

import "sync/atomic"

type TaskIntakeController struct {
	quiesced atomic.Bool
	clock    *TrustedClock
}

func NewTaskIntakeController(clock *TrustedClock) *TaskIntakeController {
	return &TaskIntakeController{clock: clock}
}

func (controller *TaskIntakeController) SetQuiesced(isQuiesced bool) {
	controller.quiesced.Store(isQuiesced)
}

func (controller *TaskIntakeController) IsQuiesced() bool {
	if controller == nil {
		return false
	}
	return controller.quiesced.Load() || controller.isWaitingForTheClock()
}

func (controller *TaskIntakeController) isWaitingForTheClock() bool {
	return controller.clock != nil && !controller.clock.IsTrusted()
}
