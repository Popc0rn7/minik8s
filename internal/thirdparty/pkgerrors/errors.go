package errors

import (
	stderrors "errors"
	"fmt"
)

// New returns an error with the supplied message.
func New(message string) error {
	return stderrors.New(message)
}

// Errorf formats according to a format specifier and returns an error.
func Errorf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

// Wrap annotates err with a message.
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

// Wrapf annotates err with a formatted message.
func Wrapf(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}

// WithMessage annotates err with a message.
func WithMessage(err error, message string) error {
	return Wrap(err, message)
}

// WithMessagef annotates err with a formatted message.
func WithMessagef(err error, format string, args ...interface{}) error {
	return Wrapf(err, format, args...)
}

// WithStack returns err unchanged. Stack rendering is not needed by Minik8s.
func WithStack(err error) error {
	return err
}

// Cause unwraps err to its root cause.
func Cause(err error) error {
	for {
		unwrapped := stderrors.Unwrap(err)
		if unwrapped == nil {
			return err
		}
		err = unwrapped
	}
}

// Is reports whether any error in err's tree matches target.
func Is(err, target error) bool {
	return stderrors.Is(err, target)
}

// As finds the first error in err's tree that matches target.
func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}

// Frame and StackTrace keep compatibility with packages that mention the
// github.com/pkg/errors stack types.
type Frame uintptr

type StackTrace []Frame
