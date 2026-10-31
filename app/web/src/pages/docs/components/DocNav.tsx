import type { DocEntry } from '../../../api';

interface Props {
  docs: DocEntry[];
  selected: string | null;
  onSelect: (name: string) => void;
}

export default function DocNav({ docs, selected, onSelect }: Props) {
  return (
    <div style={{ width: 200, paddingRight: 12, borderRight: '1px solid #eee' }}>
      <h3 style={{ margin: '0 0 12px', fontSize: 14 }}>Documentation</h3>
      {docs.map((d) => (
        <div
          key={d.name}
          onClick={() => onSelect(d.name)}
          style={{
            padding: '6px 10px',
            marginBottom: 2,
            borderRadius: 4,
            cursor: 'pointer',
            background: selected === d.name ? '#e8f0fe' : 'transparent',
            fontSize: 13,
          }}
        >
          {d.title}
        </div>
      ))}
    </div>
  );
}
