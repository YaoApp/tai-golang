package types

// Disposition indicates what happened to a Followup/Steer message.
type Disposition string

const (
	DispositionQueued  Disposition = "queued"  // enqueued, waiting to be consumed
	DispositionHandled Disposition = "handled" // consumed by input handler (e.g. skill expansion)
)
