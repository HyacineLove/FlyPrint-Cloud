import React from 'react';
import { Tooltip, Typography } from 'antd';
import { Link } from 'react-router-dom';

const TIME_ZONE = 'Asia/Shanghai';

export const compactIdentifier = (value?: string) => {
  if (!value) return '-';
  if (value.length <= 18) return value;
  return `${value.slice(0, 8)}…${value.slice(-6)}`;
};

export const CompactIdentifier: React.FC<{ value?: string; className?: string }> = ({ value, className }) => {
  if (!value) return <>-</>;
  return (
    <Tooltip title={value}>
      <Typography.Text
        className={className || 'fp-compact-id'}
        copyable={{ text: value, tooltips: ['复制完整标识', '已复制'] }}
      >
        {compactIdentifier(value)}
      </Typography.Text>
    </Tooltip>
  );
};

export const FullIdentifier: React.FC<{ value?: string }> = ({ value }) => {
  if (!value) return <>-</>;
  return <Typography.Text className="fp-full-id" copyable={{ text: value }}>{value}</Typography.Text>;
};

export const EntityCell: React.FC<{
  primary?: React.ReactNode;
  secondary?: React.ReactNode;
  id?: string;
  showId?: boolean;
  linkTo?: string;
}> = ({ primary, secondary, id, showId = false, linkTo }) => {
  const main = primary || (id ? compactIdentifier(id) : '-');
  return (
    <span className="fp-entity-cell">
      <span className="fp-entity-primary">
        {linkTo ? <Link to={linkTo}>{main}</Link> : main}
      </span>
      {secondary ? <span className="fp-entity-secondary">{secondary}</span> : null}
      {showId && id && primary ? <CompactIdentifier value={id} /> : null}
    </span>
  );
};

export const TwoLineValue: React.FC<{ id?: string; name?: string }> = ({ id, name }) => {
  return <EntityCell primary={name || (!id ? '-' : undefined)} id={id} />;
};

/** Whole name block is one jump target; an unnamed resource falls back to a compact identifier. */
export const TwoLineLink: React.FC<{ to: string; id?: string; name?: string }> = ({ to, id, name }) => {
  if (!id) return <TwoLineValue id={id} name={name} />;
  return <EntityCell primary={name || compactIdentifier(id)} id={name ? id : undefined} linkTo={to} />;
};

export const DateTimeValue: React.FC<{ value?: string | Date }> = ({ value }) => {
  if (!value) return <>-</>;
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return <>-</>;
  const dateText = new Intl.DateTimeFormat('en-CA', { timeZone: TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' }).format(date);
  const timeText = new Intl.DateTimeFormat('en-GB', { timeZone: TIME_ZONE, hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date);
  return (
    <span style={{ display: 'inline-block', lineHeight: 1.45 }}>
      <div>{dateText}</div>
      <div style={{ color: '#8c8c8c', fontSize: 12 }}>{timeText}</div>
    </span>
  );
};
