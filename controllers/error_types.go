package controllers

import (
	"errors"
	"fmt"
)

// ErrorConditions
var (
	// Generic temporary error
	ErrTemporary   = errors.New("TemporaryError")
	ErrInvalidSpec = errors.New("InvalidSpec")
	ErrClientK8s   = errors.New("k8sAPIClientError")
)

type reconcileError interface {
	error
	Wrap(msg string) reconcileError
	Unwrap() error
	Temporary() bool
}

// applicationError wraps an error condition and gives it more context from where it occurred
type applicationError struct {
	innerException error
	errorCondition error
}

func newApplicationError(inner, condition error) reconcileError {
	return &applicationError{
		innerException: inner,
		errorCondition: condition,
	}
}

func (e *applicationError) Wrap(msg string) reconcileError {
	return newApplicationError(fmt.Errorf("%s: %w", msg, e.innerException), e.errorCondition)
}

func (e *applicationError) Unwrap() error { return e.errorCondition }
func (e *applicationError) Error() string {
	if e.innerException == nil {
		return e.errorCondition.Error()
	}
	return e.innerException.Error()
}

// Temporary shows if the error condition is temporary or permanent
func (e *applicationError) Temporary() bool {
	// NOTE: List all permanent errors
	switch e.errorCondition {
	case ErrInvalidSpec:
		return false
	default:
		return true
	}
}
