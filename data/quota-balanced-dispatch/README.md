# quota-balanced-dispatch

Evidence for Section 2: Ground behavior in current Firstmate quota-balanced behavior.

## Firstmate HEAD SHA

```
82a79439d1d6f4ce2c16e0a25a7fff027b6abe0d
```

The Firstmate implementation at commit `5140413 feat(bin): make dispatch profiles quota aware (#867)`
introduced quota-aware dispatch selection in `bin/fm-dispatch-select.sh`.

Key behaviors grounded from Firstmate:

1. **Provider mapping:** Harness names map to quota provider identifiers
   (claude→claude, codex→codex, pi→pi, grok→grok).

2. **General window scoring:** Each provider's score is the minimum
   `percentRemaining` across its GENERAL quota windows
   (claude: five_hour, seven_day; codex: five_hour, weekly;
   pi: five_hour, seven_day; grok: five_hour, seven_day).
   Model-scoped windows ("model:*") are excluded.

3. **Fresh/stale distinction:** A provider with stale-but-cached data
   wins only if its minimum is at least 20 points higher than the best
   fresh candidate's minimum.

4. **Deterministic fallback:** When quota-axi is unavailable (not on PATH,
   exits non-zero, returns unparseable JSON, or no candidates are scorable),
   selection falls back to the first candidate (deterministic first-match).

5. **No core lifecycle changes:** Adapter plugs into the existing
   dispatch seam (`SelectProfile` / `ResolveDispatchSelection`) without
   modifying spawn, harness detection, or soldier lifecycle.

## Adapter design

- `QuotaAxiProvider` interface: sources quota data (real CLI or test fixture)
- `quotaBalancedSelector` struct: uses provider data to pick least-constrained
- `firstMatchSelector` struct: deterministic fallback (first profile wins)
- Both implementations unexported; interface widened only when two real
  provider adapters exist
