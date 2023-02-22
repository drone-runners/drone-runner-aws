package nomad

type JobStatus int

const (
	Unknown JobStatus = iota
	Pending
	Running
	Dead
)

func (s JobStatus) String() string {
	switch s {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Dead:
		return "dead"
	}
	return "unknown"
}

func Status(s string) JobStatus {
	switch s {
	case "pending":
		return Pending
	case "running":
		return Running
	case "dead":
		return Dead
	}
	return Unknown
}
