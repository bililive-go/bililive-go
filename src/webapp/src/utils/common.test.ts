import Utils from '../utils/common';

/**
 * Test the replaceIllegalChar function
 */
describe('Utils.replaceIllegalChar', () => {
    test('should replace illegal characters with underscores', () => {
        // Main test case from the issue
        expect(Utils.replaceIllegalChar('主播/测试')).toBe('主播_测试');
        
        // Test all illegal characters
        expect(Utils.replaceIllegalChar('test/file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test\\file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test:file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test*file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test?file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test"file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test<file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test>file')).toBe('test_file');
        expect(Utils.replaceIllegalChar('test|file')).toBe('test_file');
        
        // Test trailing spaces and dots
        expect(Utils.replaceIllegalChar('test file  ')).toBe('test file_');
        expect(Utils.replaceIllegalChar('test file..')).toBe('test file_');
        expect(Utils.replaceIllegalChar('test file. ')).toBe('test file_');
        
        // Test normal names are not changed
        expect(Utils.replaceIllegalChar('normalname')).toBe('normalname');
        expect(Utils.replaceIllegalChar('正常名字')).toBe('正常名字');
        
        // Test multiple illegal characters
        expect(Utils.replaceIllegalChar('te/st\\fi:le')).toBe('te_st_fi_le');
    });
});