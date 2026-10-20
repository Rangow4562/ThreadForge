import { describe, expect, it } from 'vitest';
import '../../src/adapters/claude.js';
import { Assembler } from '../../src/background/assembler.js';
import type { InterceptEvent } from '../../src/shared/types.js';

function request(reqId: number): InterceptEvent {
  return {
    kind: 'request', reqId, method: 'POST',
    url: 'https://claude.ai/api/organizations/org/chat_conversations/conv/completion',
    body: '{}',
  };
}

describe('bounded browser stream assembly', () => {
  it('limits concurrent parser state to four streams per tab and twelve total', () => {
    const assembler = new Assembler();
    for (let id = 1; id <= 4; id++) assembler.handle('claude.ai', 1, request(id));
    expect(assembler.active).toBe(4);
    expect(assembler.handle('claude.ai', 1, request(5))).toBeNull();
    for (let tab = 2; tab <= 3; tab++) {
      for (let id = 1; id <= 4; id++) assembler.handle('claude.ai', tab, request(id));
    }
    expect(assembler.active).toBe(12);
    expect(assembler.handle('claude.ai', 4, request(1))).toBeNull();
  });

  it('releases idle streams without marking them complete', () => {
    const assembler = new Assembler();
    assembler.handle('claude.ai', 1, request(1));
    expect(assembler.active).toBe(1);
    expect(assembler.expireIdle(Date.now() + 1_000, 500)).toEqual([]);
    expect(assembler.active).toBe(0);
  });
});
