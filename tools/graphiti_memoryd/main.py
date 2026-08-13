from __future__ import annotations

import asyncio
import hashlib
import json
import os
import shutil
import threading
import traceback
import urllib.error
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import requests_unixsocket
from graphiti_core.llm_client.client import LLMClient
from graphiti_core.llm_client.config import LLMConfig


class CapabilityLLMClient(LLMClient):
    def __init__(self, endpoint: str, model: str):
        super().__init__(LLMConfig(api_key="capability", model=model, small_model=model))
        self.endpoint = endpoint.rstrip("/")

    async def _generate_response(
        self,
        messages: list[Any],
        response_model: type[Any] | None = None,
        max_tokens: int = 8192,
        model_size: Any = None,
    ) -> dict[str, Any]:
        schema = response_model.model_json_schema() if response_model else None
        schema_name = getattr(response_model, "__name__", "graphiti_response") if response_model else "graphiti_response"
        request_document = {
            "model": self.model,
            "executionMode": os.environ.get("BLUECLAW_GRAPHITI_EXECUTION_MODE", "auto"),
            "messages": [dump_message(message) for message in messages],
            "structuredOutputSchema": {
                "name": schema_name,
                "document": schema,
                "isStrictlyEnforced": True,
            },
            "maxTokens": max_tokens,
        }
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/llm/structured",
            request_document,
        )
        content = response_document.get("content", "")
        if isinstance(content, str):
            return json.loads(content)
        if isinstance(content, dict):
            return content
        return {"content": str(content)}


class CapabilityEmbedder:
    def __init__(self, endpoint: str):
        from graphiti_core.embedder.client import EmbedderConfig

        self.endpoint = endpoint.rstrip("/")
        self.config = EmbedderConfig()

    async def create(self, input_data: str | list[str] | Any) -> list[float]:
        if not isinstance(input_data, str):
            input_data = json.dumps(input_data, ensure_ascii=False)
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/embedding/create",
            {
                "input": input_data,
                "executionMode": os.environ.get("BLUECLAW_GRAPHITI_EMBEDDING_EXECUTION_MODE", "auto"),
                "task": "retrieval",
                "inputType": "query",
            },
        )
        embedding = response_document.get("embedding", [])
        return [float(value) for value in embedding]

    async def create_batch(self, input_data_list: list[str]) -> list[list[float]]:
        response_document = await asyncio.to_thread(
            post_json,
            self.endpoint + "/v1/embedding/create",
            {
                "input": input_data_list,
                "executionMode": os.environ.get("BLUECLAW_GRAPHITI_EMBEDDING_EXECUTION_MODE", "auto"),
                "task": "retrieval",
                "inputType": "document",
            },
        )
        embeddings = response_document.get("embeddings", [])
        return [[float(value) for value in embedding] for embedding in embeddings]


class CapabilityReranker:
    def __init__(self, endpoint: str):
        self.endpoint = endpoint.rstrip("/")

    async def rank(self, query: str, passages: list[str]) -> list[tuple[str, float]]:
        if len(passages) == 0:
            return []
        try:
            response_document = await asyncio.to_thread(
                post_json,
                self.endpoint + "/v1/rerank/score",
                {"query": query, "passages": passages},
            )
        except Exception:
            return [(passage, 1.0 / (index + 1)) for index, passage in enumerate(passages)]
        ranked_passages = response_document.get("rankedPassages", [])
        return [(item.get("passage", ""), float(item.get("score", 0))) for item in ranked_passages]


def dump_message(message: Any) -> dict[str, str]:
    if isinstance(message, dict):
        return {
            "role": str(message.get("role", "user")),
            "content": str(message.get("content", "")),
        }
    if hasattr(message, "model_dump"):
        document = message.model_dump()
        return {
            "role": str(document.get("role", "user")),
            "content": str(document.get("content", "")),
        }
    return {
        "role": str(getattr(message, "role", "user")),
        "content": str(getattr(message, "content", message)),
    }


class GraphitiMemoryService:
    def __init__(self):
        from graphiti_core import Graphiti

        capability_endpoint = os.environ.get("BLUECLAW_CAPABILITY_ENDPOINT") or os.environ.get(
            "INTERNKIM_CAPABILITY_ENDPOINT", "http+unix://%2Frun%2Finternkim%2Fcapability.sock"
        )
        kuzu_path = os.environ.get("BLUECLAW_GRAPHITI_KUZU_PATH", "/workspace/.blueclaw/graphiti/kuzu")
        model = os.environ.get("BLUECLAW_GRAPHITI_MODEL", "google/gemini-3.1-flash-lite-preview")
        os.makedirs(os.path.dirname(kuzu_path), exist_ok=True)
        graph_driver = create_kuzu_driver(kuzu_path)
        graph_driver._database = ""
        self.graph_driver = graph_driver
        self.graphiti = Graphiti(
            graph_driver=graph_driver,
            llm_client=CapabilityLLMClient(capability_endpoint, model),
            embedder=CapabilityEmbedder(capability_endpoint),
            cross_encoder=CapabilityReranker(capability_endpoint),
        )
        self.operation_lock = threading.Lock()

    async def initialize(self):
        await ensure_kuzu_fulltext_indexes(self.graph_driver)
        await self.graphiti.build_indices_and_constraints()

    async def add_episode(self, request_document: dict[str, Any]) -> dict[str, Any]:
        with self.operation_lock:
            return await self.add_episode_locked(request_document)

    async def search(self, request_document: dict[str, Any]) -> dict[str, Any]:
        with self.operation_lock:
            return await self.search_locked(request_document)

    async def list_facts(self, request_document: dict[str, Any]) -> dict[str, Any]:
        with self.operation_lock:
            return await self.list_facts_locked(request_document)

    async def delete_episode(self, request_document: dict[str, Any]) -> dict[str, Any]:
        with self.operation_lock:
            return await self.delete_episode_locked(request_document)

    async def add_episode_locked(self, request_document: dict[str, Any]) -> dict[str, Any]:
        from graphiti_core.nodes import EpisodeType

        episode_id = request_document["episodeID"]
        prompt = request_document["prompt"]
        sender_person_id = request_document["senderPersonID"]
        occurred_at = parse_datetime(request_document.get("occurredAt"))
        source_reference = request_document.get("sourceReference", "")
        namespaces = request_document.get("namespaces", [])
        for namespace in namespaces:
            namespace_id = namespace["namespaceID"]
            await self.graphiti.add_episode(
                name=graphiti_group_id(episode_id + ":" + namespace_id),
                episode_body=episode_body_for_namespace(namespace, sender_person_id, prompt),
                source=EpisodeType.message,
                source_description=source_reference,
                reference_time=occurred_at,
                group_id=graphiti_group_id(namespace_id),
                custom_extraction_instructions=extraction_instructions_for_namespace(namespace, sender_person_id),
            )
        return {"episodeID": episode_id, "namespaceCount": len(namespaces)}

    async def delete_episode_locked(self, request_document: dict[str, Any]) -> dict[str, Any]:
        episode_id = str(request_document.get("episodeID", "")).strip()
        namespace_ids = request_document.get("namespaceIDs", [])
        deleted_count = 0
        for namespace_id in namespace_ids:
            deleted_count += await self.delete_namespace_episode(episode_id, str(namespace_id).strip())
        return {"episodeID": episode_id, "deleted": deleted_count > 0, "namespaceCount": deleted_count}

    async def delete_namespace_episode(self, episode_id: str, namespace_id: str) -> int:
        from graphiti_core.nodes import EpisodicNode

        if episode_id == "" or namespace_id == "":
            return 0
        expected_name = graphiti_group_id(episode_id + ":" + namespace_id)
        group_id = graphiti_group_id(namespace_id)
        episodes = await EpisodicNode.get_by_group_ids(self.graphiti.driver, [group_id], limit=1000)
        for episode in episodes:
            if str(getattr(episode, "name", "") or "") != expected_name:
                continue
            result = self.graphiti.remove_episode(str(getattr(episode, "uuid", "")))
            if hasattr(result, "__await__"):
                await result
            return 1
        return 0

    async def search_locked(self, request_document: dict[str, Any]) -> dict[str, Any]:
        from graphiti_core.search.search_config_recipes import COMBINED_HYBRID_SEARCH_RRF

        query = request_document.get("Query") or request_document.get("query") or ""
        limit = int(request_document.get("Limit") or request_document.get("limit") or 12)
        namespaces = request_document.get("Namespaces") or request_document.get("namespaces") or []
        facts: list[dict[str, Any]] = []
        for namespace in namespaces:
            namespace_id = namespace["namespaceID"]
            namespace_facts: list[dict[str, Any]] = []
            results = await self.graphiti.search(query=query, group_ids=[graphiti_group_id(namespace_id)], num_results=limit)
            for result in results:
                namespace_facts.append(
                    {
                        "factID": getattr(result, "uuid", ""),
                        "scopeType": namespace.get("scopeType", ""),
                        "namespaceID": namespace_id,
                        "content": getattr(result, "fact", ""),
                        "score": float(getattr(result, "score", 0) or 0),
                        "sourceEpisodeID": getattr(result, "source_node_uuid", ""),
                        "sourceKind": "fact",
                        "validAt": serialize_datetime(getattr(result, "valid_at", None)),
                        "securityLevelRank": namespace.get("securityLevelRank", 0),
                        "requiredClasses": namespace.get("requiredClasses", []),
                    }
                )
            if len(namespace_facts) < limit:
                search_results = await self.graphiti.search_(
                    query=query,
                    config=COMBINED_HYBRID_SEARCH_RRF,
                    group_ids=[graphiti_group_id(namespace_id)],
                )
                namespace_facts.extend(facts_from_search_results(search_results, namespace, limit-len(namespace_facts)))
            facts.extend(namespace_facts)
        return {"facts": facts}

    async def list_facts_locked(self, request_document: dict[str, Any]) -> dict[str, Any]:
        limit = int(request_document.get("Limit") or request_document.get("limit") or 50)
        namespaces = request_document.get("Namespaces") or request_document.get("namespaces") or []
        facts: list[dict[str, Any]] = []
        for namespace in namespaces:
            namespace_id = namespace["namespaceID"]
            group_id = graphiti_group_id(namespace_id)
            facts.extend(await self.list_namespace_facts(namespace, namespace_id, group_id, limit))
        return {"facts": facts}

    async def list_namespace_facts(self, namespace: dict[str, Any], namespace_id: str, group_id: str, limit: int) -> list[dict[str, Any]]:
        from graphiti_core.edges import EntityEdge
        from graphiti_core.nodes import EntityNode

        facts: list[dict[str, Any]] = []
        try:
            edges = await EntityEdge.get_by_group_ids(self.graphiti.driver, [group_id], limit=limit)
        except Exception:
            edges = []
        for edge in edges:
            content = str(getattr(edge, "fact", "") or "").strip()
            if content == "":
                continue
            facts.append(memory_fact(namespace, namespace_id, getattr(edge, "uuid", ""), content, "fact"))
        try:
            nodes = await EntityNode.get_by_group_ids(self.graphiti.driver, [group_id], limit=limit)
        except Exception:
            nodes = []
        for node in nodes:
            name = str(getattr(node, "name", "") or "").strip()
            summary = str(getattr(node, "summary", "") or "").strip()
            content = " ".join(value for value in [name, summary] if value)
            if content == "":
                continue
            facts.append(memory_fact(namespace, namespace_id, getattr(node, "uuid", ""), content, "node"))
        return facts


class LazyGraphitiMemoryService:
    def __init__(self):
        self.service: GraphitiMemoryService | None = None
        self.service_lock = threading.Lock()

    async def get_service(self) -> GraphitiMemoryService:
        with self.service_lock:
            if self.service is None:
                service = GraphitiMemoryService()
                await service.initialize()
                self.service = service
            return self.service

    async def add_episode(self, request_document: dict[str, Any]) -> dict[str, Any]:
        service = await self.get_service()
        return await service.add_episode(request_document)

    async def search(self, request_document: dict[str, Any]) -> dict[str, Any]:
        service = await self.get_service()
        return await service.search(request_document)

    async def list_facts(self, request_document: dict[str, Any]) -> dict[str, Any]:
        service = await self.get_service()
        return await service.list_facts(request_document)

    async def delete_episode(self, request_document: dict[str, Any]) -> dict[str, Any]:
        service = await self.get_service()
        return await service.delete_episode(request_document)


def graphiti_group_id(namespace_id: str) -> str:
    digest = hashlib.sha256(namespace_id.encode("utf-8")).hexdigest()[:24]
    return "bc_" + digest


def create_kuzu_driver(kuzu_path: str) -> KuzuDriver:
    from graphiti_core.driver.kuzu_driver import KuzuDriver

    try:
        return KuzuDriver(db=kuzu_path)
    except Exception as error:
        if not is_kuzu_open_corruption(error):
            raise
        quarantine_kuzu_store(kuzu_path)
        return KuzuDriver(db=kuzu_path)


def is_kuzu_open_corruption(error: Exception) -> bool:
    message = str(error).lower()
    return "unordered_map::at" in message or ("metadata" in message and "wal" in message)


def quarantine_kuzu_store(kuzu_path: str):
    suffix = datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S")
    for path in [kuzu_path, kuzu_path + ".wal"]:
        if not os.path.exists(path):
            continue
        quarantine_path = path + ".corrupt." + suffix
        shutil.move(path, quarantine_path)


def episode_body_for_namespace(namespace: dict[str, Any], sender_person_id: str, prompt: str) -> str:
    if namespace.get("scopeType") == "user":
        return (
            "Blueclaw user "
            + sender_person_id
            + " made this first-person statement. Interpret first-person pronouns as Blueclaw user "
            + sender_person_id
            + ": "
            + prompt
        )
    return sender_person_id + ": " + prompt


def extraction_instructions_for_namespace(namespace: dict[str, Any], sender_person_id: str) -> str:
    if namespace.get("scopeType") == "user":
        return (
            "For this user namespace, extract durable facts about Blueclaw user "
            + sender_person_id
            + ". Treat first-person pronouns such as I, me, my, 내, 나, 저, 제 as this same user. "
            + "If the user states their name or preferred name, create a fact that this Blueclaw user's name is that value."
        )
    if namespace.get("scopeType") == "workspace":
        return "Extract durable company, team, policy, project, process, and operational facts only."
    return "Extract durable facts that are only useful inside this conversation context."


async def ensure_kuzu_fulltext_indexes(graph_driver: KuzuDriver):
    from graphiti_core.driver.driver import GraphProvider
    from graphiti_core.graph_queries import get_fulltext_indices

    for query in get_fulltext_indices(GraphProvider.KUZU):
        try:
            await graph_driver.execute_query(query)
        except Exception as error:
            if "already exists" not in str(error).lower():
                raise


def facts_from_search_results(search_results: Any, namespace: dict[str, Any], limit: int) -> list[dict[str, Any]]:
    facts: list[dict[str, Any]] = []
    namespace_id = namespace["namespaceID"]
    for node in getattr(search_results, "nodes", []):
        name = str(getattr(node, "name", "") or "").strip()
        summary = str(getattr(node, "summary", "") or "").strip()
        content = " ".join(value for value in [name, summary] if value)
        if content == "":
            continue
        facts.append(memory_fact(namespace, namespace_id, getattr(node, "uuid", ""), content, "node"))
        if len(facts) >= limit:
            return facts
    return facts


def memory_fact(namespace: dict[str, Any], namespace_id: str, fact_id: str, content: str, source_kind: str) -> dict[str, Any]:
    return {
        "factID": source_kind + ":" + fact_id,
        "scopeType": namespace.get("scopeType", ""),
        "namespaceID": namespace_id,
        "content": content,
        "score": 0,
        "sourceEpisodeID": fact_id,
        "sourceKind": source_kind,
        "validAt": zero_time(),
        "securityLevelRank": namespace.get("securityLevelRank", 0),
        "requiredClasses": namespace.get("requiredClasses", []),
    }


def post_json(url: str, request_document: dict[str, Any]) -> dict[str, Any]:
    if url.startswith("http+unix://"):
        session = requests_unixsocket.Session()
        response = session.post(url, json=request_document, timeout=30)
        if response.status_code >= 400:
            raise RuntimeError(response.text)
        if response.text.strip() == "":
            return {}
        return response.json()

    request_body = json.dumps(request_document).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=request_body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as error:
        body = error.read().decode("utf-8")
        raise RuntimeError(body)
    if body.strip() == "":
        return {}
    return json.loads(body)


def parse_datetime(value: str | None) -> datetime:
    if not value:
        return datetime.now(timezone.utc)
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def serialize_datetime(value: Any) -> str:
    if isinstance(value, datetime):
        return value.astimezone(timezone.utc).isoformat()
    return zero_time()


def zero_time() -> str:
    return "0001-01-01T00:00:00Z"


class RequestHandler(BaseHTTPRequestHandler):
    service: GraphitiMemoryService

    def do_GET(self):
        if self.path == "/health":
            self.write_json(200, {"status": "ok"})
            return
        self.write_json(404, {"error": "not found"})

    def do_POST(self):
        try:
            request_document = self.read_json()
            if self.path == "/v1/episodes":
                response_document = asyncio.run(self.service.add_episode(request_document))
            elif self.path == "/v1/episodes/delete":
                response_document = asyncio.run(self.service.delete_episode(request_document))
            elif self.path == "/v1/search":
                response_document = asyncio.run(self.service.search(request_document))
            elif self.path == "/v1/list":
                response_document = asyncio.run(self.service.list_facts(request_document))
            else:
                self.write_json(404, {"error": "not found"})
                return
            self.write_json(200, response_document)
        except Exception as error:
            traceback.print_exc()
            self.write_json(500, {"error": str(error), "traceback": traceback.format_exc()})

    def read_json(self) -> dict[str, Any]:
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length).decode("utf-8")
        if body.strip() == "":
            return {}
        return json.loads(body)

    def write_json(self, status_code: int, document: dict[str, Any]):
        body = json.dumps(document).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *arguments):
        return


def main():
    listen_address = os.environ.get("BLUECLAW_GRAPHITI_LISTEN_ADDRESS", "127.0.0.1")
    listen_port = int(os.environ.get("BLUECLAW_GRAPHITI_PORT", "7791"))
    RequestHandler.service = LazyGraphitiMemoryService()
    server = ThreadingHTTPServer((listen_address, listen_port), RequestHandler)
    server.serve_forever()


if __name__ == "__main__":
    main()
