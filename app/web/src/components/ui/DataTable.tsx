import type { ReactNode } from 'react';

export interface Column<T> {
  key: string;
  header: ReactNode;
  render: (row: T) => ReactNode;
  /** Renders the cell in monospace, for ids and timestamps. */
  mono?: boolean;
  /** Emphasizes the cell as the row's identity (first column). */
  primary?: boolean;
  width?: number | string;
}

interface Props<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  empty?: ReactNode;
}

/**
 * Table with the shared header and row styling.
 *
 * @param columns cell renderers, in display order
 * @param rowKey stable React key; a row id is the natural choice
 * @param empty rendered instead of the table when there are no rows
 * @returns a `.data-table` wrapper
 */
export default function DataTable<T>({ columns, rows, rowKey, empty }: Props<T>) {
  if (rows.length === 0 && empty) {
    return <div className="empty">{empty}</div>;
  }

  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key} style={c.width ? { width: c.width } : undefined}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((c) => {
                const classes: string[] = [];
                if (c.primary) classes.push('table__cell--primary');
                if (c.mono) classes.push('table__cell--mono');
                return (
                  <td key={c.key} className={classes.length ? classes.join(' ') : undefined}>
                    {c.render(row)}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
