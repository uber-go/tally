// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package tally

import (
	"fmt"
)

const (
	_resultType        = "result_type"
	_resultTypeError   = "error"
	_resultTypeSuccess = "success"
	_timingFormat      = "%s.latency"
)

// NewInstrumentedCall returns an InstrumentedCall with the given name
func NewInstrumentedCall(scope Scope, name string) InstrumentedCall {
	return &instrumentedCall{
		error:   scope.Tagged(map[string]string{_resultType: _resultTypeError}).Counter(name),
		success: scope.Tagged(map[string]string{_resultType: _resultTypeSuccess}).Counter(name),
		timing:  scope.Timer(fmt.Sprintf(_timingFormat, name)),
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
