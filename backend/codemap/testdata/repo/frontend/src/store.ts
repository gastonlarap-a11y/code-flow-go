import { api } from "./api";

export interface StoreState {
  load: () => Promise<void>;
}

function create<T>(build: () => T): (select: (state: T) => unknown) => unknown {
  const state = build();
  return (select) => select(state);
}

export const useStore = create<StoreState>(() => ({
  load: async () => {
    await api.list();
  },
}));

export const table = { selectFrom: (name: string) => name };

export function readCarts(): string {
  table.selectFrom("carts");
  return "SELECT name FROM items WHERE id = ?";
}
