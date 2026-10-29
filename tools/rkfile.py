"""Read and write Rangka metadata files (.rk).

A .rk file is YAML, restricted so it means the same thing in every tool:

- YAML 1.2 core types only: true/false, null, decimal ints, floats. Dates stay strings.
- Values that YAML 1.1 tools read differently are refused unless quoted:
  yes/no/on/off/y/n, numbers with a leading zero (phone numbers, NPWP, item codes), 1_000, 1:30.
- Duplicate keys, tabs for indentation, anchors/aliases and merge keys (<<) are refused.
- One document per file.

load(path) returns (data, where) where where(path_tuple) -> "line:col" for error messages.
Legacy .json files load through the same function so tools can read both during migration.
"""
import json, os, re
import yaml

EXTS = (".rk", ".rk.yaml", ".rk.yml", ".json")
AMBIGUOUS = re.compile(r"^(?:y|Y|n|N|yes|Yes|YES|no|No|NO|on|On|ON|off|Off|OFF|[-+]?0[0-9_]+(?:\.[0-9]*)?|[-+]?[0-9][0-9_]*_[0-9_]*|[-+]?[0-9]+(?::[0-5]?[0-9])+(?:\.[0-9]*)?)$")


class RkError(Exception):
    def __init__(self, path, line, col, code, msg):
        super().__init__(f"{path}:{line}:{col} {code}: {msg}")
        self.path, self.line, self.col, self.code, self.msg = path, line, col, code, msg


def _core12(cls):
    cls.yaml_implicit_resolvers = {}
    cls.add_implicit_resolver("tag:yaml.org,2002:bool", re.compile(r"^(?:true|True|TRUE|false|False|FALSE)$"), list("tTfF"))
    cls.add_implicit_resolver("tag:yaml.org,2002:null", re.compile(r"^(?:~|null|Null|NULL|)$"), ["~", "n", "N", ""])
    cls.add_implicit_resolver("tag:yaml.org,2002:int", re.compile(r"^(?:[-+]?(?:0|[1-9][0-9]*)|0x[0-9a-fA-F]+)$"), list("-+0123456789"))
    cls.add_implicit_resolver("tag:yaml.org,2002:float", re.compile(r"^(?:[-+]?(?:\.[0-9]+|(?:0|[1-9][0-9]*)(?:\.[0-9]*)?)(?:[eE][-+]?[0-9]+)?|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$"), list("-+0123456789."))
    return cls


@_core12
class _Loader(yaml.SafeLoader):
    pass


def _bool(loader, node):
    return node.value.lower() == "true"


_Loader.add_constructor("tag:yaml.org,2002:bool", _bool)


@_core12
class _Dumper(yaml.SafeDumper):
    def increase_indent(self, flow=False, indentless=False):
        return super().increase_indent(flow, False)


# make the writer quote anything the reader would refuse or read as a non-string
_Dumper.add_implicit_resolver("tag:rangka,ambiguous", AMBIGUOUS, list("yYnNoO-+0123456789"))


def _str(dumper, s):
    style = "|" if "\n" in s else '"' if s[:1] in "'\"" else None
    return dumper.represent_scalar("tag:yaml.org,2002:str", s, style=style)


def _flow_ok(v):
    if isinstance(v, dict):
        return 0 < len(v) <= 4 and all(isinstance(x, (str, int, float, bool)) or x is None for x in v.values()) and len(json.dumps(v, ensure_ascii=False)) <= 72
    if isinstance(v, list):
        return 0 < len(v) <= 8 and all(isinstance(x, (str, int, float)) for x in v) and len(json.dumps(v, ensure_ascii=False)) <= 72
    return False


def _dict(dumper, d):
    return dumper.represent_mapping("tag:yaml.org,2002:map", d.items(), flow_style=_flow_ok(d))


def _list(dumper, l):
    return dumper.represent_sequence("tag:yaml.org,2002:seq", l, flow_style=_flow_ok(l))


_Dumper.add_representer(str, _str)
_Dumper.add_representer(dict, _dict)
_Dumper.add_representer(list, _list)


def _walk(node, path, where, problems):
    where[path] = (node.start_mark.line + 1, node.start_mark.column + 1)
    if isinstance(node, yaml.MappingNode):
        seen = {}
        for k, v in node.value:
            if k.value == "<<":
                problems.append((k, "merge_key", "merge keys (<<) are not allowed; repeat the fields or use a template"))
                continue
            if k.value in seen:
                problems.append((k, "duplicate_key", f'"{k.value}" appears twice (first on line {seen[k.value]})'))
            seen[k.value] = k.start_mark.line + 1
            _walk(v, path + (k.value,), where, problems)
    elif isinstance(node, yaml.SequenceNode):
        for i, v in enumerate(node.value):
            _walk(v, path + (i,), where, problems)
    elif isinstance(node, yaml.ScalarNode) and node.style is None and node.tag == "tag:yaml.org,2002:str" and AMBIGUOUS.match(node.value):
        hint = "true or false" if node.value.lower() in ("y", "n", "yes", "no", "on", "off") else "quotes for a time like 08:30" if ":" in node.value else "a number without leading zeros"
        problems.append((node, "ambiguous_value", f'{node.value} means different things in different YAML tools; write "{node.value}" in quotes for text, or {hint}'))


def loads(text, path="<string>"):
    if "\t" in text.split("\n", 1)[0] or re.search(r"^\t", text, re.M):
        line = next(i for i, l in enumerate(text.split("\n"), 1) if l.startswith("\t"))
        raise RkError(path, line, 1, "tab_indent", "indent with spaces, not tabs")
    loader = _Loader(text)
    try:
        node = loader.get_single_node()
        if node is None:
            return {}, lambda p: "1:1"
        for n in _anchors(node):
            raise RkError(path, n.start_mark.line + 1, n.start_mark.column + 1, "anchor", "anchors and aliases (&, *) are not allowed; .rk files are read top to bottom")
        where, problems = {}, []
        _walk(node, (), where, problems)
        if problems:
            n, code, msg = problems[0]
            err = RkError(path, n.start_mark.line + 1, n.start_mark.column + 1, code, msg)
            err.all = [RkError(path, x.start_mark.line + 1, x.start_mark.column + 1, c, m) for x, c, m in problems]
            raise err
        data = loader.construct_document(node)
    except yaml.MarkedYAMLError as e:
        m = e.problem_mark or e.context_mark
        raise RkError(path, m.line + 1 if m else 1, m.column + 1 if m else 1, "yaml_syntax", (e.problem or e.context or "invalid YAML").strip())
    finally:
        loader.dispose()

    def at(p):
        p = tuple(p)
        while p and p not in where:
            p = p[:-1]
        l, c = where.get(p, (1, 1))
        return f"{l}:{c}"
    return data, at


def _anchors(node, seen=None):
    seen = set() if seen is None else seen
    if id(node) in seen:
        yield node
        return
    seen.add(id(node))
    if getattr(node, "anchor", None):
        yield node
    kids = [x for kv in node.value for x in kv] if isinstance(node, yaml.MappingNode) else node.value if isinstance(node, yaml.SequenceNode) else []
    for k in kids:
        yield from _anchors(k, seen)


def load(path):
    text = open(path, encoding="utf-8").read()
    if path.endswith(".json"):
        try:
            return json.loads(text), lambda p: "?"
        except json.JSONDecodeError as e:
            raise RkError(path, e.lineno, e.colno, "json_syntax", e.msg)
    return loads(text, path)


def dumps(data, schema_url=None):
    body = yaml.dump(data, Dumper=_Dumper, sort_keys=False, allow_unicode=True, width=120, indent=2, default_flow_style=False)
    head = f"# yaml-language-server: $schema={schema_url}\n" if schema_url else ""
    # keys are always text in .rk, so on:/off:/yes: keys stay plain (values are still quoted)
    body = re.sub(r"^(\s*(?:- )?)'(y|n|yes|no|on|off)':", r"\1\2:", body, flags=re.M | re.I)
    return head + body


def find(directory, stem):
    """Return the metadata file for stem in directory (stem.rk, stem.rk.yaml, stem.rk.yml or legacy stem.json)."""
    for ext in EXTS:
        p = os.path.join(directory, stem + ext)
        if os.path.isfile(p):
            return p
    return None


def stem(fn):
    for ext in EXTS:
        if fn.endswith(ext):
            return fn[: -len(ext)]
    return None


def listdir(directory):
    """(stem, path) for every metadata file in directory, sorted, .rk preferred over legacy .json."""
    out = {}
    for fn in sorted(os.listdir(directory)) if os.path.isdir(directory) else []:
        s = stem(fn)
        if s and (s not in out or out[s].endswith(".json")):
            out[s] = os.path.join(directory, fn)
    return sorted(out.items())
