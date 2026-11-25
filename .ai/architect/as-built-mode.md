# As-Built Documentation Mode

This mode applies ONLY to an explicit Owner request for retrospective
(as-built) documentation of an already implemented change. It overrides the
standard design workflow and output requirements of the architect role for
that request.

- Document the implemented architecture; do not propose or plan
  implementation.
- ADR status may be IMPLEMENTED (as-built, commit <examined commit>).
  This status records implementation evidence, not architectural approval.
- Use the Owner-specified architecture*.md and decisions*.md output paths
  under .ai/architect/. Do not overwrite existing non-empty files.
- Do not create implementation tasks, submit documents for review, or
  change workflow state.
- Report DOCUMENTED when the requested documents are complete and
  traceable, or BLOCKED when missing evidence or an output collision
  prevents completion. Do not report ARCHITECTED for this mode.
- Mark reconstructed rationale as INFERENCE. Preserve Finding IDs and
  statuses; record unresolved questions without modifying the findings
  registry.

All other evidence, scope, and safety requirements remain applicable.
This exception applies only to an explicit Owner request for as-built
documentation; it does not authorize production changes or imply approval.
