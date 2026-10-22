package util

// This file held the ExtractNewOutput/suffixPrefixOverlap oracle, fuzz, and
// benchmark suite. Those helpers were superseded by the pipeline's stricter
// agentDeliveryNewOutput (4ee45168), which requires line-anchored boundaries
// and reports a lost boundary instead of returning a lenient byte-overlap
// delta; wiring the lenient helper back would silently change response-evidence
// semantics. ntm-1ko3j deleted the helpers and this suite. The file is
// retained rather than removed per the repository's no-file-deletion rule.
