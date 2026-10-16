// Swagger UI for the NETRA integration API: "Authorize" takes the client_id and client_secret
// (client credentials flow) and then every "Try it out" call carries the access token.
window.ui = SwaggerUIBundle({
  url: '/api/ext/v1/openapi.yaml',
  dom_id: '#swagger-ui',
  deepLinking: true,
  tryItOutEnabled: true,
  displayRequestDuration: true,
  defaultModelsExpandDepth: 1,
  docExpansion: 'list',
});
