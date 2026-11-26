package composer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestEvaluateReimportConflict_TypedAttrs pins that the re-import conflict
// check reads typed Attrs, which it skipped before #921.
func TestEvaluateReimportConflict_TypedAttrs(t *testing.T) {
	t.Parallel()
	edit := imported.FieldEdit{
		Source:   imported.SourceImporter,
		EditedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		OldValue: float64(30),
		NewValue: float64(60),
	}

	_, conflict := evaluateReimportConflict("f", "visibility_timeout_seconds", edit, typedSQS(t, "q", 30, ""))
	require.True(t, conflict, "desired 30 diverged from the edit's 60")

	_, conflict = evaluateReimportConflict("f", "visibility_timeout_seconds", edit, typedSQS(t, "q", 60, ""))
	require.False(t, conflict, "desired matches the edit")

	// A reference edit matches a typed expression by its source text.
	refEdit := edit
	refEdit.OldValue = nil
	refEdit.NewValue = "aws_kms_key.queues.arn"
	_, conflict = evaluateReimportConflict("f", "kms_master_key_id", refEdit, typedSQS(t, "q", 30, "aws_kms_key.queues.arn"))
	require.False(t, conflict, "desired expression matches the edit")
}
