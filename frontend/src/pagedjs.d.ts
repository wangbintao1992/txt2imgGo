declare module "pagedjs" {
  export class Previewer {
    constructor(options?: Record<string, unknown>);
    preview(
      content: Element | string,
      stylesheets?: Array<string | Record<string, string>>,
      renderTo?: Element,
    ): Promise<unknown>;
    on(event: string, cb: (...args: unknown[]) => void): void;
  }
}
