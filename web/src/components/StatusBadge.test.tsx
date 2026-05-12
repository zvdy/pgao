import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import StatusBadge from './StatusBadge';

describe('StatusBadge', () => {
  it('maps healthy → ok class', () => {
    const { container } = render(<StatusBadge status="healthy" />);
    expect(container.firstChild).toHaveClass('badge', 'ok');
    expect(container.firstChild).toHaveTextContent('healthy');
  });

  it('maps degraded → warn class', () => {
    const { container } = render(<StatusBadge status="degraded" />);
    expect(container.firstChild).toHaveClass('warn');
  });

  it('maps connecting → pending class', () => {
    const { container } = render(<StatusBadge status="connecting" />);
    expect(container.firstChild).toHaveClass('pending');
  });

  it('falls back to bad class for unknown / unhealthy statuses', () => {
    const { container, rerender } = render(<StatusBadge status="unhealthy" />);
    expect(container.firstChild).toHaveClass('bad');

    rerender(<StatusBadge status="anything-else" />);
    expect(container.firstChild).toHaveClass('bad');
  });
});
