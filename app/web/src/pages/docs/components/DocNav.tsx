import type { DocEntry } from '../../../api';

interface Props {
  docs: DocEntry[];
  selected: string | null;
  onSelect: (name: string) => void;
}

export default function DocNav({ docs, selected, onSelect }: Props) {
  return (
    <div className="doc-nav">
      <h3 className="doc-nav__title">Documentation</h3>
      <div className="doc-nav__rows">
        {docs.map((d) => (
          <button
            key={d.name}
            type="button"
            onClick={() => onSelect(d.name)}
            className={`doc-nav__item${selected === d.name ? ' doc-nav__item--active' : ''}`}
          >
            {d.title}
          </button>
        ))}
      </div>
    </div>
  );
}
