interface Props {
  status: string;
}

export default function StatusBadge({ status }: Props) {
  const cls =
    status === 'healthy'
      ? 'ok'
      : status === 'degraded'
        ? 'warn'
        : status === 'connecting'
          ? 'pending'
          : 'bad';
  return <span className={`badge ${cls}`}>{status}</span>;
}
