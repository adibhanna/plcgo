// Package result provides a Result type for consistent error handling.
// This implements the Result pattern similar to Rust's Result or the dark-matter library.
package result

// Result represents either a success value or an error.
type Result[T any] struct {
	value T
	err   error
	ok    bool
}

// Ok creates a successful Result with the given value.
func Ok[T any](value T) Result[T] {
	return Result[T]{value: value, ok: true}
}

// Err creates a failed Result with the given error.
func Err[T any](err error) Result[T] {
	return Result[T]{err: err, ok: false}
}

// IsOk returns true if the Result contains a success value.
func (r Result[T]) IsOk() bool {
	return r.ok
}

// IsErr returns true if the Result contains an error.
func (r Result[T]) IsErr() bool {
	return !r.ok
}

// Unwrap returns the success value, panicking if the Result is an error.
func (r Result[T]) Unwrap() T {
	if !r.ok {
		panic("called Unwrap on an Err Result")
	}
	return r.value
}

// UnwrapOr returns the success value or the provided default.
func (r Result[T]) UnwrapOr(defaultValue T) T {
	if r.ok {
		return r.value
	}
	return defaultValue
}

// UnwrapErr returns the error, panicking if the Result is Ok.
func (r Result[T]) UnwrapErr() error {
	if r.ok {
		panic("called UnwrapErr on an Ok Result")
	}
	return r.err
}

// Error returns the error or nil if the Result is Ok.
func (r Result[T]) Error() error {
	return r.err
}

// Value returns the value and a boolean indicating success.
func (r Result[T]) Value() (T, bool) {
	return r.value, r.ok
}

// Map transforms the success value using the provided function.
func Map[T, U any](r Result[T], f func(T) U) Result[U] {
	if r.ok {
		return Ok(f(r.value))
	}
	return Err[U](r.err)
}

// FlatMap transforms the success value using a function that returns a Result.
func FlatMap[T, U any](r Result[T], f func(T) Result[U]) Result[U] {
	if r.ok {
		return f(r.value)
	}
	return Err[U](r.err)
}
