import { expect as playwrightExpect, test as playwrightTest } from '@playwright/test';

type TestBody = () => void | Promise<void>;

export const describe = (title: string, body: () => void): void => playwrightTest.describe(title, body);
export const it = (title: string, body: TestBody): void => {
  playwrightTest(title, body);
};
export const beforeAll = (body: TestBody): void => {
  playwrightTest.beforeAll(body);
};
export const afterAll = (body: TestBody): void => {
  playwrightTest.afterAll(body);
};
export const setTestTimeout = (timeout: number): void => playwrightTest.setTimeout(timeout);
export const expect = playwrightExpect;
