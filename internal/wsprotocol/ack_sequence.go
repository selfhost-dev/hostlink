package wsprotocol

type AckPayload struct {
	AckedMessageID        string      `json:"acked_message_id"`
	AckedType             MessageType `json:"acked_type"`
	TaskID                string      `json:"task_id,omitempty"`
	ExecutionAttemptID    string      `json:"execution_attempt_id,omitempty"`
	HighestOutputSequence *int        `json:"highest_output_sequence,omitempty"`
}

type AckOptions struct {
	AckedMessageID        string
	AckedType             MessageType
	TaskID                string
	ExecutionAttemptID    string
	HighestOutputSequence *int
}

type ErrorPayload struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	Retryable        bool   `json:"retryable"`
	RelatedMessageID string `json:"related_message_id,omitempty"`
}

type ErrorOptions struct {
	Code             string
	Message          string
	Retryable        bool
	RelatedMessageID string
}

type MessageRecordResult int

const (
	MessageAccepted MessageRecordResult = iota
	MessageDuplicate
)

type MessageTracker struct {
	processed map[string]struct{}
}

type SequenceStatus int

const (
	SequenceAccepted SequenceStatus = iota
	SequenceDuplicate
	SequenceGap
)

type SequenceResult struct {
	Status                SequenceStatus
	HighestOutputSequence int
	ExpectedSequence      int
}

type OutputSequenceTracker struct {
	highest map[sequenceKey]int
}

type sequenceKey struct {
	executionAttemptID string
	stream             Stream
}

func BuildAck(opts AckOptions) AckPayload {
	return AckPayload{
		AckedMessageID:        opts.AckedMessageID,
		AckedType:             opts.AckedType,
		TaskID:                opts.TaskID,
		ExecutionAttemptID:    opts.ExecutionAttemptID,
		HighestOutputSequence: opts.HighestOutputSequence,
	}
}

func BuildError(opts ErrorOptions) ErrorPayload {
	return ErrorPayload{
		Code:             opts.Code,
		Message:          opts.Message,
		Retryable:        opts.Retryable,
		RelatedMessageID: opts.RelatedMessageID,
	}
}

func NewMessageTracker() *MessageTracker {
	return &MessageTracker{processed: make(map[string]struct{})}
}

func (t *MessageTracker) Record(messageID string) MessageRecordResult {
	if _, ok := t.processed[messageID]; ok {
		return MessageDuplicate
	}

	t.processed[messageID] = struct{}{}
	return MessageAccepted
}

func NewOutputSequenceTracker() *OutputSequenceTracker {
	return &OutputSequenceTracker{highest: make(map[sequenceKey]int)}
}

func (t *OutputSequenceTracker) Record(executionAttemptID string, stream Stream, sequence int) SequenceResult {
	key := sequenceKey{executionAttemptID: executionAttemptID, stream: stream}
	highest := t.highest[key]
	expected := highest + 1

	if sequence == expected {
		t.highest[key] = sequence
		return SequenceResult{Status: SequenceAccepted, HighestOutputSequence: sequence, ExpectedSequence: sequence + 1}
	}

	if sequence <= highest {
		return SequenceResult{Status: SequenceDuplicate, HighestOutputSequence: highest, ExpectedSequence: expected}
	}

	return SequenceResult{Status: SequenceGap, HighestOutputSequence: highest, ExpectedSequence: expected}
}

func (r SequenceResult) Retryable() bool {
	return r.Status == SequenceGap
}
