import assert from "node:assert/strict";
import { createServer } from "node:http";
import test from "node:test";
import { SearchClient } from "/usr/src/app/dist/src/infrastructure/client/SearchClient.js";

const logger = { info() {}, error() {} };

async function searchThroughLocalHTTP(input, responseBody) {
  let capturedRequest;
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    capturedRequest = {
      method: request.method,
      url: request.url,
      authorization: request.headers.authorization,
      body: JSON.parse(Buffer.concat(chunks).toString("utf8")),
    };
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify(responseBody));
  });

  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  const address = server.address();
  const client = new SearchClient(
    { baseUrl: `http://127.0.0.1:${address.port}/bitbucket/context`, token: "fixture-token" },
    logger,
  );

  try {
    const result = await client.searchBitbucketContent(input);
    return { capturedRequest, result };
  } finally {
    await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  }
}

test("scoped code search uses the Data Center route and preserves the raw result envelope", async () => {
  const responseBody = { code: { count: 0, values: [], isLastPage: true } };
  const { capturedRequest, result } = await searchThroughLocalHTTP(
    { workspaceSlug: "PRJ", query: "needle", scope: "sample-repo" },
    responseBody,
  );

  assert.equal(capturedRequest.method, "POST");
  assert.equal(capturedRequest.url, "/bitbucket/context/rest/search/latest/search");
  assert.equal(capturedRequest.authorization, "Bearer fixture-token");
  assert.deepEqual(capturedRequest.body, {
    query: "needle project:PRJ repo:sample-repo",
    entities: { code: { start: 0, limit: 10 } },
  });
  assert.equal(result.content[0].type, "text");
  assert.deepEqual(JSON.parse(result.content[0].text), responseBody);
});

test("unscoped search keeps its global query and maps advertised filters to Data Center modifiers", async () => {
  const responseBody = { code: { count: 1, values: [{ file: "src/example.xml" }], isLastPage: true } };
  const { capturedRequest } = await searchThroughLocalHTTP(
    { workspaceSlug: "PRJ", query: "alpha OR beta", extension: "xml", language: "java" },
    responseBody,
  );

  assert.equal(capturedRequest.url, "/bitbucket/context/rest/search/latest/search");
  assert.equal(capturedRequest.body.query, "alpha OR beta ext:xml lang:java");
  assert.deepEqual(capturedRequest.body.entities, { code: { start: 0, limit: 10 } });
});
