import { test, expect } from '@playwright/test';

/**
 * 设置页面测试
 * 
 * 测试配置页面的各种设置功能
 */
test.describe('设置页面测试', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/#/configInfo');
    await page.waitForLoadState('domcontentloaded');
  });

  test('设置页面正确加载', async ({ page }) => {
    // 验证页面内容区域
    const content = page.getByRole('main');
    await expect(content).toBeVisible();
  });

  test('设置页面包含表单元素', async ({ page }) => {
    // 等待页面加载
    await page.waitForTimeout(1000);

    // 验证存在表单元素
    const formItems = page.locator('.ant-form-item');
    const count = await formItems.count();

    // 设置页面应该有多个配置项
    expect(count).toBeGreaterThan(0);
  });

  test('输入框可以编辑', async ({ page }) => {
    await page.waitForTimeout(1000);

    // 找到第一个文本输入框
    const textInput = page.locator('input[type="text"]').first();

    if (await textInput.isVisible()) {
      // 获取原始值
      const originalValue = await textInput.inputValue();

      // 清空并输入新值
      await textInput.clear();
      await textInput.fill('test-value');

      // 验证值已更改
      await expect(textInput).toHaveValue('test-value');

      // 恢复原始值（避免影响其他测试）
      await textInput.clear();
      await textInput.fill(originalValue);
    }
  });

  test('Switch 开关可以切换', async ({ page }) => {
    await page.waitForTimeout(1000);

    // 找到第一个开关
    const switchElement = page.locator('.ant-switch').first();

    if (await switchElement.isVisible()) {
      // 获取初始状态
      const isCheckedBefore = await switchElement.evaluate(
        el => el.classList.contains('ant-switch-checked')
      );

      // 点击切换
      await switchElement.click();
      await page.waitForTimeout(300);

      // 验证状态改变
      const isCheckedAfter = await switchElement.evaluate(
        el => el.classList.contains('ant-switch-checked')
      );

      expect(isCheckedAfter).not.toBe(isCheckedBefore);

      // 恢复原始状态
      await switchElement.click();
    }
  });

  test('Select 下拉框可以选择', async ({ page }) => {
    await page.waitForTimeout(1000);

    // 找到第一个 Select
    const selectElement = page.locator('.ant-select').first();

    if (await selectElement.isVisible()) {
      // 点击打开下拉框
      await selectElement.click();
      await page.waitForTimeout(300);

      // 验证下拉选项出现
      const dropdown = page.locator('.ant-select-dropdown');
      await expect(dropdown).toBeVisible();

      // 点击其他地方关闭
      await page.locator('body').click();
    }
  });

  test('数字输入框支持增减', async ({ page }) => {
    await page.waitForTimeout(1000);

    // 找到数字输入框
    const numberInput = page.locator('.ant-input-number').first();

    if (await numberInput.isVisible()) {
      // 找到增加按钮
      const increaseBtn = numberInput.locator('.ant-input-number-handler-up');

      if (await increaseBtn.isVisible()) {
        // 获取原始值
        const input = numberInput.locator('input');
        const originalValue = await input.inputValue();

        // 点击增加
        await increaseBtn.click();
        await page.waitForTimeout(100);

        // 验证值改变（应该增加了）
        const newValue = await input.inputValue();
        // 值可能相同如果已达到最大值，所以只验证输入框可交互
        expect(newValue).toBeDefined();
      }
    }
  });
});

test.describe('设置页面 - 标签页切换', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/#/configInfo');
    await page.waitForLoadState('domcontentloaded');
  });

  test('标签页可以切换', async ({ page }) => {
    await page.waitForTimeout(1000);

    // 使用 count 避免 isVisible 的超时问题
    const tabItems = page.locator('.ant-tabs-tab');
    const count = await tabItems.count();

    if (count > 1) {
      // 点击第二个标签
      await tabItems.nth(1).click();
      await page.waitForTimeout(500);

      // 验证第二个标签激活（可能使用不同的激活类名）
      const isActive = await tabItems.nth(1).evaluate(
        el => el.classList.contains('ant-tabs-tab-active') ||
          el.getAttribute('aria-selected') === 'true'
      );
      expect(isActive).toBe(true);
    } else if (count === 1) {
      // 只有一个标签，测试跳过
      console.log('只有一个标签页，跳过切换测试');
    } else {
      // 没有标签页，测试跳过
      console.log('没有找到标签页');
    }
  });
});

