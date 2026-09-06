import importlib.util
from pathlib import Path
import sys
import types
import unittest
from datetime import datetime, timezone


def load_module():
    for module_name in [
        "graphiti_core",
        "graphiti_core.cross_encoder.client",
        "graphiti_core.embedder.client",
        "graphiti_core.llm_client.client",
        "graphiti_core.llm_client.config",
        "requests_unixsocket",
    ]:
        sys.modules[module_name] = types.ModuleType(module_name)
    sys.modules["graphiti_core.cross_encoder.client"].CrossEncoderClient = object
    sys.modules["graphiti_core.embedder.client"].EmbedderClient = object
    sys.modules["graphiti_core.embedder.client"].EmbedderConfig = object
    sys.modules["graphiti_core.llm_client.client"].LLMClient = object
    sys.modules["graphiti_core.llm_client.config"].LLMConfig = object
    specification = importlib.util.spec_from_file_location("graphiti_memoryd", Path(__file__).with_name("main.py"))
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    return module


class ProvenanceTests(unittest.TestCase):
    def test_structured_episode_source_and_temporal_projection(self):
        module = load_module()
        episode = types.SimpleNamespace(
            uuid="episode-uuid",
            name=module.graphiti_episode_name("local-episode", "user:one"),
            source_description=module.episode_source_description("local-episode", "post-1"),
        )
        edge = types.SimpleNamespace(
            uuid="fact-uuid",
            episodes=["episode-uuid"],
            valid_at=datetime(2026, 1, 1, tzinfo=timezone.utc),
            created_at=datetime(2026, 1, 2, tzinfo=timezone.utc),
            invalid_at=None,
            expired_at=None,
        )
        sources = module.episode_source_mapping([episode], "user:one")
        fact = module.memory_fact({}, "user:one", "fact-uuid", "prefers tea", "fact", edge, sources)
        self.assertEqual(fact["sourceEpisodeID"], "local-episode")
        self.assertEqual(fact["sourceEpisodeIDs"], ["local-episode"])
        self.assertEqual(fact["validAt"], "2026-01-01T00:00:00+00:00")
        self.assertEqual(fact["recordedAt"], "2026-01-02T00:00:00+00:00")

    def test_old_hashed_episode_name_does_not_become_fact_source(self):
        module = load_module()
        episode = types.SimpleNamespace(uuid="episode-uuid", name=module.graphiti_group_id("local-episode:user:one"), source_description="")
        self.assertEqual(module.episode_source_mapping([episode], "user:one"), {})


if __name__ == "__main__":
    unittest.main()
