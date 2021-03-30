package controllers

import (
	"errors"
	"fmt"
)

// ErrorConditions
var (
	// Generic temporary error
	ErrTemporary   = errors.New("GenericTemporaryError")
	ErrPermanent   = errors.New("GenericPermanentError")
	ErrInvalidSpec = errors.New("InvalidSpec")
	// ErrFunctionDomain can be returned by a function that was passed invalid arguments, such as the default case of a switch.
	// Consider constraining the arg type.
	ErrFunctionDomain         = errors.New("FunctionDomainError")
	ErrClientK8s              = errors.New("k8sAPIClientError")
	ErrPodExec                = errors.New("ExecInPodError")
	ErrFilesystemIO           = errors.New("FilesystemIOError")
	ErrDBOD                   = errors.New("DBODError")
	ErrBuildFailed            = errors.New("BuildError")
	ErrDeploymentUpdateFailed = errors.New("DeploymentUpdateError")
	ErrDBUpdateFailed         = errors.New("DBUpdateError")
)

type reconcileError interface {
	error
	Wrap(msg string) reconcileError
	Unwrap() error
	Temporary() bool
}

// concat concatenates the Error of each reconcileError and sets the result's errorCondition to the first condition
func concat(errs []reconcileError) reconcileError {
	if len(errs) == 0 {
		return nil
	}
	sum := errors.New("")
	for _, e := range errs {
		sum = fmt.Errorf("%v\n%v", sum, e)
	}
	return newApplicationError(sum, errs[0].Unwrap())
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
	return fmt.Errorf("%s: %v", e.errorCondition.Error(), e.innerException).Error()
}

// Temporary shows if the error condition is temporary or permanent
func (e *applicationError) Temporary() bool {
	// NOTE: List all permanent errors
	switch e.errorCondition {
	case ErrInvalidSpec:
		return false
	case ErrFunctionDomain:
		return false
	case ErrPermanent:
		return false
	case ErrBuildFailed:
		return false
	default:
		return true
	}
}
