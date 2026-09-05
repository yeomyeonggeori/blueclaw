import tempfile
import threading
import unittest
from datetime import datetime, timezone
from types import SimpleNamespace

from graphiti_core.driver.kuzu_driver import KuzuDriver
from graphiti_core.edges import EntityEdge
from graphiti_core.errors import EdgeNotFoundError
from graphiti_core.nodes import EntityNode, EpisodeType, EpisodicNode

from main import GraphitiMemoryService, graphiti_episode_name, graphiti_group_id


class DocumentEmbedder:
    def __init__(self):
        self.documents = []

    async def create_batch(self, documents):
        self.documents.extend(documents)
        return [[0.9, 0.8] for document in documents]


class GraphitiPersistenceTests(unittest.IsolatedAsyncioTestCase):
    async def test_fact_mutations_preserve_provenance_and_survive_reopening(self):
        with tempfile.TemporaryDirectory(prefix="blueclaw-graphiti-test-") as directory:
            database_path = directory + "/database"
            driver = KuzuDriver(db=database_path)
            await driver.build_indices_and_constraints()
            namespace_id = "user:sample-person"
            group_id = graphiti_group_id(namespace_id)
            timestamp = datetime.now(timezone.utc)
            source = EntityNode(name="source", group_id=group_id)
            target = EntityNode(name="target", group_id=group_id)
            await source.save(driver)
            await target.save(driver)
            episode = EpisodicNode(
                name=graphiti_episode_name("sample-episode", namespace_id),
                group_id=group_id, source=EpisodeType.message,
                source_description="", content="sample source", valid_at=timestamp,
            )
            await episode.save(driver)
            edge = EntityEdge(
                name="preference", fact="prefers tea", group_id=group_id,
                source_node_uuid=source.uuid, target_node_uuid=target.uuid,
                episodes=[episode.uuid], created_at=timestamp, valid_at=timestamp,
                fact_embedding=[0.1, 0.2],
            )
            await edge.save(driver)
            embedder = DocumentEmbedder()
            service = self.service(driver, embedder)
            request = {"namespaceID": namespace_id, "factID": "fact:" + edge.uuid}
            updated = await service.update_fact({**request, "content": "prefers coffee"})
            self.assertEqual(updated["content"], "prefers coffee")
            self.assertEqual(updated["scopeType"], "user")
            self.assertEqual(updated["sourceEpisodeIDs"], ["sample-episode"])
            self.assertEqual(embedder.documents, ["prefers coffee"])
            with self.assertRaises(ValueError):
                await service.update_fact({**request, "namespaceID": "user:other", "content": "changed"})
            with self.assertRaises(ValueError):
                await service.update_fact({**request, "content": " "})
            await driver.close()
            del service, driver
            reopened = KuzuDriver(db=database_path)
            try:
                records = await EntityEdge.get_by_group_ids(reopened, [group_id], with_embeddings=True)
                persisted = next(record for record in records if record.uuid == edge.uuid)
                self.assertEqual(persisted.fact, "prefers coffee")
                for actual, expected in zip(persisted.fact_embedding, [0.9, 0.8], strict=True):
                    self.assertAlmostEqual(actual, expected, places=5)
                service = self.service(reopened, embedder)
                with self.assertRaises(ValueError):
                    await service.delete_fact({**request, "namespaceID": "user:other"})
                self.assertTrue((await service.delete_fact(request))["deleted"])
                with self.assertRaises(EdgeNotFoundError):
                    await EntityEdge.get_by_uuid(reopened, edge.uuid)
                self.assertEqual((await EpisodicNode.get_by_uuid(reopened, episode.uuid)).content, "sample source")
            finally:
                await reopened.close()

    def service(self, driver, embedder):
        service = object.__new__(GraphitiMemoryService)
        service.graphiti = SimpleNamespace(driver=driver, embedder=embedder)
        service.operation_lock = threading.Lock()
        return service


if __name__ == "__main__":
    unittest.main()
