"""Structure-aware knowledge chunks, with compatibility for older string chunks."""
from llama_index.core.node_parser import SentenceSplitter
from llama_index.core.utils import get_tokenizer
from markdown_it import MarkdownIt

SIZE = 384
OVERLAP = 48
markdown = MarkdownIt("commonmark").enable("table")


def text(chunk):
    return chunk if isinstance(chunk, str) else chunk["text"]


def section(chunk):
    return "" if isinstance(chunk, str) else chunk.get("section", "")


def context_prefix(title, path_value):
    tokenizer = get_tokenizer()
    title, path_value = title[:120], path_value[:120]
    while len(tokenizer("文档：" + title + "\n章节：" + path_value)) > 128:
        if len(path_value) > len(title):
            path_value = path_value[:-1]
        else:
            title = title[:-1]
    return "文档：" + title + "\n" + ("章节：" + path_value + "\n" if path_value else "") + "\n"


def embedding_text(title, chunk):
    if isinstance(chunk, str):
        return chunk
    return context_prefix(title, section(chunk)) + text(chunk)


def split(content, title):
    tokenizer = get_tokenizer()
    lines = content.splitlines(keepends=True)
    headings = {}
    result = []
    pending = []
    pending_path = ""

    def path():
        return " / ".join(headings.values())[:120]

    def budget(path_value):
        prefix = context_prefix(title, path_value)
        return max(1, SIZE - len(tokenizer(prefix)))

    def add(value, path_value):
        value = value.strip()
        if value:
            result.append({"text": value, "section": path_value})

    def flush():
        nonlocal pending
        if pending:
            add("\n\n".join(pending), pending_path)
            pending = []

    def add_block(block, path_value):
        nonlocal pending, pending_path
        limit = budget(path_value)
        candidate = "\n\n".join([*pending, block])
        if pending and (pending_path != path_value or len(tokenizer(candidate)) > limit):
            flush()
        if len(tokenizer(block)) > limit:
            flush()
            splitter = SentenceSplitter(chunk_size=limit, chunk_overlap=min(OVERLAP, max(0, limit // 4)))
            for part in splitter.split_text(block):
                add(part, path_value)
        else:
            pending_path = path_value
            pending.append(block)

    for token in markdown.parse(content):
        if token.level != 0 or token.map is None:
            continue
        raw = "".join(lines[token.map[0]:token.map[1]]).strip()
        if token.type == "heading_open":
            flush()
            level = int(token.tag[1:])
            headings = {key: value for key, value in headings.items() if key < level}
            inline = next((t for t in markdown.parse(raw) if t.type == "inline"), None)
            headings[level] = (inline.content if inline else raw).strip()[:120]
        elif token.type == "table_open":
            flush()
            table_lines = raw.splitlines()
            if len(table_lines) < 3:
                add_block(raw, path())
                continue
            header = "\n".join(table_lines[:2])
            table = header
            for row in table_lines[2:]:
                candidate = table + "\n" + row
                if len(tokenizer(candidate)) > budget(path()) and table != header:
                    add(table, path())
                    table = header + "\n" + row
                else:
                    table = candidate
                if len(tokenizer(table)) > budget(path()):
                    add_block(table, path())
                    flush()
                    table = header
            if table != header:
                add(table, path())
        elif token.nesting != -1:
            add_block(raw, path())
    flush()
    if not result:
        add_block(content, "")
        flush()
    return result
