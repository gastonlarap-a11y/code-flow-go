import { useStore } from "./store";
import { addDirect } from "./api";

function ItemRow() {
  return <li>row</li>;
}

export function useItems() {
  return useStore((s) => s.load);
}

/** ItemList shows every item. */
export function ItemList() {
  const load = useItems();
  const onAdd = () => addDirect();
  return (
    <ul onClick={onAdd}>
      <ItemRow />
    </ul>
  );
}
