package tally

import (
	"fmt"
)

// NewInstrumentedCall returns an InstrumentedCall with the given name
func NewInstrumentedCall(scope Scope, name string) InstrumentedCall {
	return &instrumentedCall{
		error:   scope.Tagged(map[string]string{"result_type": "error"}).Counter(name),
		success: scope.Tagged(map[string]string{"result_type": "success"}).Counter(name),
		timing:  scope.Timer(fmt.Sprintf("%s.latency", name)),
	}
}

var defaultSuccessFilter = func(err error) bool {
	return err == nil
}

type instrumentedCall struct {
	scope   Scope
	success Counter
	error   Counter
	timing  Timer
}

// Exec executes the given block of code, and records whether it succeeded or
// failed, and the amount of time that it took
func (c *instrumentedCall) Exec(f ExecFn) error {
	return c.ExecWithFilter(f, defaultSuccessFilter)
}

// ExecWithFilter executes the given block of code, and records whether it succeeded or
// failed based on the result of a custom filter (e.g. the filter could determine a bad request error
// to be actually success for server logic), and the amount of time that it took
func (c *instrumentedCall) ExecWithFilter(f ExecFn, isSuccess SuccessFilterFn) error {
	sw := c.timing.Start()

	err := f()
	if err != nil && !isSuccess(err) {
		c.error.Inc(1.0)
		return err
	}

	sw.Stop()
	c.success.Inc(1.0)

	return err
}
