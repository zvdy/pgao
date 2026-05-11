import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { api, ApiError } from './api';

const fetchMock = vi.fn();
beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe('api client', () => {
  it('attaches Authorization: Bearer header when a key is in localStorage', async () => {
    localStorage.setItem('pgao.apiKey', 'sekret');
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ status: 'ok' }),
    });

    await api.ready();

    expect(fetchMock).toHaveBeenCalledOnce();
    const [, init] = fetchMock.mock.calls[0]!;
    const headers = init?.headers as Headers;
    expect(headers.get('Authorization')).toBe('Bearer sekret');
  });

  it('omits Authorization when no key is set', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ status: 'ok' }),
    });

    await api.ready();

    const [, init] = fetchMock.mock.calls[0]!;
    const headers = init?.headers as Headers;
    expect(headers.has('Authorization')).toBe(false);
  });

  it('surfaces backend error message + status on non-OK responses', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 412,
      statusText: 'Precondition Failed',
      json: async () => ({ error: 'pg_stat_statements extension is not installed' }),
    });

    let err: unknown;
    try {
      await api.getSlowQueries('c1');
    } catch (e) {
      err = e;
    }
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(412);
    expect((err as ApiError).message).toContain('pg_stat_statements');
  });

  it('encodes cluster IDs that contain reserved characters', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({}),
    });

    await api.getCluster('prod/cluster-1');

    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toBe('/api/v1/clusters/prod%2Fcluster-1');
  });

  it('falls back to statusText when the error body is not JSON', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: async () => {
        throw new Error('not json');
      },
    });

    let err: unknown;
    try {
      await api.ready();
    } catch (e) {
      err = e;
    }
    expect((err as ApiError).message).toBe('Internal Server Error');
  });
});
