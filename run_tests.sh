#!/bin/bash

echo "🧪 Running OGS Notifications Server Tests"
echo "========================================"

echo ""
echo "🔒 Security Tests:"
echo "------------------"
go test -v -run="TestInputValidation|TestErrorResponse|TestXSS" -timeout=30s
if [ $? -ne 0 ]; then
    echo "❌ Security tests failed"
    exit 1
fi

echo ""
echo "⚡ Functionality Tests:"
echo "----------------------"
go test -v -run="TestRegistration|TestTurnDetection|TestNotification|TestDiagnostics" -timeout=30s
if [ $? -ne 0 ]; then
    echo "❌ Functionality tests failed"
    exit 1
fi

echo ""
echo "💾 Storage Tests:"
echo "----------------"
go test -v -run="TestStorage|TestFile|TestConcurrent|TestMigration" -timeout=30s
if [ $? -ne 0 ]; then
    echo "❌ Storage tests failed"
    exit 1
fi

echo ""
echo "✅ All Tests Passed!"
echo "===================="
echo "🔒 Security: Input validation, error sanitization, XSS prevention"
echo "⚡ Functionality: Registration, turn detection, notifications"
echo "💾 Storage: Persistence, concurrent access, migration"
echo ""
echo "🚀 Server is ready for production hardening!"