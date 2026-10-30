import unittest

from llama_index.core.utils import get_tokenizer
import document_chunks as chunks


class StructureChunks(unittest.TestCase):
    def test_sections_preserve_heading_context_without_changing_source(self):
        content = "# 公司报告\n\n## 财务\n\n### 上半年\n\n营业收入增长。\n\n## 风险\n\n负债较高。"
        result = chunks.split(content, "国轩高科报告")
        self.assertEqual([c["section"] for c in result], ["公司报告 / 财务 / 上半年", "公司报告 / 风险"])
        self.assertEqual([c["text"] for c in result], ["营业收入增长。", "负债较高。"])
        self.assertIn("文档：国轩高科报告\n章节：公司报告 / 财务 / 上半年", chunks.embedding_text("国轩高科报告", result[0]))

    def test_large_table_repeats_header_and_preserves_rows(self):
        header = "| 项目 | 数值 |\n| --- | --- |"
        rows = [f"| 第{i}项 | {i}亿元 |" for i in range(100)]
        result = chunks.split("# 财报\n\n" + header + "\n" + "\n".join(rows), "报告")
        self.assertGreater(len(result), 1)
        self.assertTrue(all(c["text"].startswith(header) for c in result))
        self.assertEqual([line for c in result for line in c["text"].splitlines()[2:]], rows)
        self.assertTrue(all(len(get_tokenizer()(chunks.embedding_text("报告", c))) <= chunks.SIZE for c in result))

    def test_fenced_code_does_not_create_heading_and_long_text_is_bounded(self):
        content = "# 正文\n\n```md\n# 不是章节\n```\n\n" + "财务增长。" * 500
        result = chunks.split(content, "报告")
        self.assertGreater(len(result), 1)
        self.assertTrue(all(c["section"] == "正文" for c in result))
        self.assertTrue(all(len(get_tokenizer()(chunks.embedding_text("报告", c))) <= chunks.SIZE for c in result))
        self.assertEqual(chunks.text("旧片段"), "旧片段")
        self.assertEqual(chunks.embedding_text("旧文档", "旧片段"), "旧片段")


if __name__ == '__main__':
    unittest.main()
