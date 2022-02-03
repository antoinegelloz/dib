package mock

import "github.com/radiofrance/dib/types"

type TestRunner struct {
	CallCount     int
	ExpectedError error
	ShouldSupport bool
}

func (t *TestRunner) Supports(_ types.RunTestOptions) bool {
	return t.ShouldSupport
}

func (t *TestRunner) RunTest(_ types.RunTestOptions) error {
	t.CallCount++
	return t.ExpectedError
}
