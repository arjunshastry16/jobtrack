// The typed API client. Paths, parameters and response bodies all come from
// docs/openapi.yaml via the generated schema.d.ts (`npm run generate`), so calling an
// endpoint that doesn't exist, or reading a field the API doesn't return, is a type
// error rather than a runtime surprise.
import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

export const api = createClient<paths>();

export type Schemas = components["schemas"];

// errorMessage turns a failed call into text to show. The API's errors all have the
// same shape ({"error": {"code", "message"}}); anything else (a proxy's HTML error page,
// a network failure) gets the HTTP status.
export function errorMessage(error: unknown, response?: Response): string {
  if (typeof error === "object" && error !== null && "error" in error) {
    const e = (error as Schemas["Error"]).error;
    // Validation errors name the fields at fault, e.g. "role: is required".
    const fields = Object.entries(e.fields ?? {}).map(([field, problem]) => `${field}: ${problem}`);
    return fields.length > 0 ? `${e.message} (${fields.join("; ")})` : e.message;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return response ? `HTTP ${response.status}` : "request failed";
}
