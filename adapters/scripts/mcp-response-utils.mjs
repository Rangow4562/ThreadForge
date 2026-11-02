const maxCandidateTextLength = 1 << 20;
const maxDepth = 8;
const maxObjectStarts = 16;
const maxParsedValues = 64;

export function findJsonObjectWithSchema(value, schemaVersion) {
  if (typeof schemaVersion !== 'string' || schemaVersion.length < 1 || schemaVersion.length > 128) return undefined;
  return collectJsonValues(value).find((candidate) => candidate?.schema_version === schemaVersion);
}

function collectJsonValues(value, depth = 0, seen = new Set(), output = []) {
  if (depth > maxDepth || value == null || output.length >= maxParsedValues) return output;
  if (typeof value === 'string') {
    if (value.length > maxCandidateTextLength) return output;
    let parsed;
    try { parsed = JSON.parse(value); } catch { parsed = undefined; }
    if (parsed !== undefined) collectJsonValues(parsed, depth + 1, seen, output);

    const starts = [];
    for (let index = 0; index < value.length && starts.length < maxObjectStarts; index++) {
      if (value[index] === '{') starts.push(index);
    }
    for (const start of starts) {
      const stack = [];
      let inString = false;
      let escaped = false;
      for (let index = start; index < value.length; index++) {
        const character = value[index];
        if (inString) {
          if (escaped) escaped = false;
          else if (character === '\\') escaped = true;
          else if (character === '"') inString = false;
          continue;
        }
        if (character === '"') { inString = true; continue; }
        if (character === '{') stack.push('}');
        else if (character === '[') stack.push(']');
        else if (character === '}' || character === ']') {
          if (stack.pop() !== character) break;
          if (stack.length === 0) {
            try {
              const candidate = JSON.parse(value.slice(start, index + 1));
              collectJsonValues(candidate, depth + 1, seen, output);
            } catch { /* Ignore non-JSON braces in provider text. */ }
            break;
          }
        }
      }
    }
    return output;
  }
  if (typeof value !== 'object' || seen.has(value)) return output;
  seen.add(value);
  output.push(value);
  if (Array.isArray(value)) {
    for (const item of value.slice(0, 32)) collectJsonValues(item, depth + 1, seen, output);
  } else {
    for (const item of Object.values(value).slice(0, 48)) collectJsonValues(item, depth + 1, seen, output);
  }
  return output;
}
