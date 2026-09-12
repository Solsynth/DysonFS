package media

import "errors"

var (
	// ErrInvalidParams is returned when transform query parameters (or a
	// referenced preset) fail validation.
	ErrInvalidParams = errors.New("invalid transform params")
	// ErrSourceUnavailable is returned when the source image cannot be
	// resolved or loaded from storage.
	ErrSourceUnavailable = errors.New("source image unavailable")
	// ErrSourceTooLarge is returned when the source image exceeds the
	// configured transform size limits.
	ErrSourceTooLarge = errors.New("source image exceeds transform size limit")
	// ErrOutputTooLarge is returned when the transformed output exceeds the
	// configured output size limit.
	ErrOutputTooLarge = errors.New("transformed output exceeds size limit")
	// ErrAnimatedUnsupported is returned for multi-page (animated) sources.
	ErrAnimatedUnsupported = errors.New("animated images are not supported")
)
