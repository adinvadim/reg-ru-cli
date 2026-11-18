## Changes

- Add `regru domain rereg catalog fetch|search`, `quote`, `bids`, and `bid` for the REG.RU backorder of releasing .ru/.рф/.su names: the public catalogue and lot data are read through a throwaway headless browser, `quote` returns the shared dropcatch quote object in kopecks, and `bid` sends one REG.API `domain/set_rereg_bids` within per-bid and daily caps (default 3000/10000 RUB), refuses the 16:55–17:15 MSK guard window and the 16:45 MSK release-day deadline with the new `policy_refused` exit code 9, and reconciles an ambiguous dispatch to `outcome_unknown` instead of resending.
