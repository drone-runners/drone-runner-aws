package nomad

type JobStatus int

const (
	Unknown JobStatus = iota
	Pending
	Running
	Dead
)

const (
	unknownStr = "unknown"
	pendingStr = "pending"
	runningStr = "running"
	deadStr    = "dead"
)

func (s JobStatus) String() string {
	switch s {
	case Pending:
		return pendingStr
	case Running:
		return runningStr
	case Dead:
		return deadStr
	case Unknown:
		return unknownStr
	}
	return unknownStr
}

func Status(s string) JobStatus {
	switch s {
	case pendingStr:
		return Pending
	case runningStr:
		return Running
	case deadStr:
		return Dead
	case unknownStr:
		return Unknown
	}
	return Unknown
}
