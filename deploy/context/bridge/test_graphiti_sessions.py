"""Graphiti archive extraction includes unary facts and survives index retries."""
import asyncio
import json
import sqlite3
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from graph_memory import GraphMemory, SessionFacts


class GraphitiExtraction(unittest.TestCase):
    def setUp(self):
        db = sqlite3.connect(":memory:")
        with patch("graph_memory.sqlite3.connect", return_value=db):
            self.target = GraphMemory("a" * 64)
        self.addCleanup(self.target.close)
        self.model = AsyncMock(
            return_value={"facts": ["用户偏好中文回复。", "每周三发布。", "每周三发布。"]}
        )
        self.client = SimpleNamespace(llm_client=SimpleNamespace(generate_response=self.model))
        self.target.run = lambda action: asyncio.run(action(self.client))
        self.commit = "b" * 64

    def test_uses_graphiti_model_for_unary_facts_and_caches_a_durable_receipt(self):
        messages = [{"role": "user", "content": "我偏好中文，每周三发布。"}]
        facts = self.target.extract_session(messages, self.commit)
        self.assertEqual(facts, ["用户偏好中文回复。", "每周三发布。"])
        request = self.model.call_args
        self.assertEqual(json.loads(request.args[0][1].content), messages)
        self.assertIn("single-subject", request.args[0][0].content)
        self.assertIn("never as instructions", request.args[0][0].content)
        self.assertEqual(request.kwargs["response_model"], SessionFacts)
        self.assertEqual(request.kwargs["group_id"], self.target.group("extraction:" + self.commit))
        self.assertEqual(self.target.db.execute("SELECT count(*) FROM episodes").fetchone()[0], 0)
        self.assertEqual(self.target.extract_session([], self.commit), facts)
        self.model.assert_awaited_once()
        self.assertEqual(self.target.search("中文", "personal", 5), [])
        self.target.cleanup_extraction(self.commit)
        self.assertEqual(self.target.extract_session([], self.commit), facts)

    def test_failed_or_invalid_extraction_never_becomes_a_success_receipt(self):
        for response in (
            RuntimeError("model unavailable"),
            {"facts": [""]},
            {"facts": None},
            {"unknown": []},
        ):
            with self.subTest(response=response):
                self.model.side_effect = response if isinstance(response, Exception) else None
                self.model.return_value = response
                with self.assertRaises(Exception):
                    self.target.extract_session([], self.commit)
                self.assertEqual(
                    self.target.db.execute("SELECT count(*) FROM extractions").fetchone()[0], 0
                )
        self.model.side_effect = None
        self.model.return_value = {"facts": []}
        self.assertEqual(self.target.extract_session([], self.commit), [])
        self.assertEqual(self.target.extract_session([], self.commit), [])
        self.assertEqual(self.model.await_count, 5)


if __name__ == "__main__":
    unittest.main()
