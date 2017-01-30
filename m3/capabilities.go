package metrics

var (
	defaultCapabilities = &capabilities{
		reporting: true,
		tagging:   false,
	}
	taggingCapabilities = &capabilities{
		reporting: true,
		tagging:   true,
	}
)

type capabilities struct {
	reporting bool
	tagging   bool
}

func (c capabilities) Reporting() bool {
	return c.reporting
}

func (c capabilities) Tagging() bool {
	return c.tagging
}
