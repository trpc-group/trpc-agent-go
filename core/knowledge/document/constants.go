package document

const (
	// DefaultChunkSize is the default maximum size of each chunk in characters.
	DefaultChunkSize = 1000

	// DefaultOverlap is the default number of characters to overlap between chunks.
	DefaultOverlap = 0

	// MinProgressRatio is the minimum progress ratio to prevent infinite loops.
	MinProgressRatio = 0.1

	// ParagraphSeparator is the string used to separate paragraphs.
	ParagraphSeparator = "\n\n"

	// CarriageReturn represents the carriage return character.
	CarriageReturn = "\r"

	// LineFeed represents the line feed character.
	LineFeed = "\n"

	// CarriageReturnLineFeed represents the Windows line ending.
	CarriageReturnLineFeed = "\r\n"
)

// Whitespace characters used for word boundary detection.
const (
	Space = " "
	Tab   = "\t"
)

// Natural break characters in priority order.
var (
	// HighPriorityBreaks are the preferred break points for natural chunking.
	HighPriorityBreaks = []string{LineFeed}

	// MediumPriorityBreaks are secondary break points for natural chunking.
	MediumPriorityBreaks = []string{"."}

	// WhitespaceChars defines characters considered as whitespace for word boundaries.
	WhitespaceChars = []rune{' ', '\n', '\r', '\t'}
)
