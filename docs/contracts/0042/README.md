# Contract 0042 fixtures

Shared by rangka-ai and rangka-cloud: copy verbatim into tests. The contract (`../0042-consultant-interview.md`) is the authority. Check with `python3 -I docs/contracts/0042/check_fixtures.py`. Shapes: map `{map, valid, error?}`; ops `{map, ops, confirmed_guesses, want_map, want_dropped}` (`index` is the 0-based position in `ops`); facts `{facts, want_map}`.

- `map-valid-rental.json`: the Sewa Pesta Makmur map (5 stages, 7 pieces: connector, catalogue, custom with design, existing) is valid.
- `map-invalid-too-many-stages.json`: 31 stages is `map_too_large`.
- `map-invalid-long-label.json`: 151 emoji (302 UTF-16 units, 151 code points) is `field_too_long`; counting runes or code points passes it wrongly.
- `map-invalid-bad-id.json`: stage id `S_Pesan` breaks `^[a-z][a-z0-9_]{0,31}$`, `bad_id`.
- `ops-basic.json`: business merge, new stage last, replace by id, put piece/open/setting, remove, confirmed guess accepted, and drops `bad_shape` (piece without `ref`, bad id, 301-character string) and `unknown_id`.
- `ops-unconfirmed-guess.json`: a setting with `from_guess` not in `confirmed_guesses` is dropped as `unconfirmed_guess`, map unchanged.
- `ops-dangling-piece.json`: stage references to pieces missing after all ops are removed silently, a piece added by a later op keeps its reference, the stage is kept.
- `ops-order.json`: listed stages first, unknown ids ignored, empty list is a no-op, ops apply in sequence.
- `ops-remove-piece.json`: removing a piece removes it from every stage (stages kept); unknown id, and an id from another collection, drop as `unknown_id`.
- `facts-to-map-complete.json`: all 13 facts to a map: trim, whitespace-only fact is absent, setting order (`data_to_move` before `pain_points`, `t_replace` last), `p_current`, 300 UTF-16 unit cut with an emoji before the cut.
- `facts-to-map-missing.json`: absent, null, non-string and blank facts are absent; `o_<key>` scripted questions for missing required facts; an unrecognised `replace_or_complement` gives neither setting nor open item.
- `facts-to-map-normalise.json`: surrounding whitespace is trimmed before use (`  Ganti penuh ` gives setting `t_replace` "Juragan: ganti penuh"; ` Kue basah ` gives summary `Kue basah`).
