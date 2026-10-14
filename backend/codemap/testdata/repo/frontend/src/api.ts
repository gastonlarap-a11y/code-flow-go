// The fixture's IPC layer: one string-keyed command, one HTTP route, one Wails binding.
import { Service } from "../bindings/example.com/shop/shop";

/** Api is what the components call. */
export interface Api {
  list(): Promise<string[]>;
  save(id: string): Promise<void>;
}

function invoke(command: string): Promise<string[]> {
  return Promise.resolve([command]);
}

export const api: Api = {
  async list() {
    return invoke("shop_list_items");
  },
  async save(id: string) {
    await fetch(`/api/items/${id}`);
  },
};

export interface ShopContract {
  AddItem(): Promise<void>;
}

// The binding satisfies the contract as a whole.
export const ShopService: ShopContract = Service;

export function addDirect(): Promise<void> {
  return Service.AddItem();
}
